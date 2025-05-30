// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package pki

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-secure-stdlib/parseutil"
	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/helper/certutil"
	"github.com/openbao/openbao/sdk/v2/helper/consts"
	"github.com/openbao/openbao/sdk/v2/helper/errutil"
	"github.com/openbao/openbao/sdk/v2/logical"
)

func pathListRoles(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "roles/?$",

		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: operationPrefixPKI,
			OperationSuffix: "roles",
		},

		Fields: map[string]*framework.FieldSchema{
			"after": {
				Type:        framework.TypeString,
				Description: `Optional entry to list begin listing after, not required to exist.`,
			},
			"limit": {
				Type:        framework.TypeInt,
				Description: `Optional number of entries to return; defaults to all entries.`,
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ListOperation: &framework.PathOperation{
				Callback: b.pathRoleList,
				Responses: map[int][]framework.Response{
					http.StatusOK: {{
						Description: "OK",
						Fields: map[string]*framework.FieldSchema{
							"keys": {
								Type:        framework.TypeStringSlice,
								Description: "List of roles",
								Required:    true,
							},
						},
					}},
				},
			},
		},

		HelpSynopsis:    pathListRolesHelpSyn,
		HelpDescription: pathListRolesHelpDesc,
	}
}

func pathRoles(b *backend) *framework.Path {
	pathRolesResponseFields := map[string]*framework.FieldSchema{
		"ttl": {
			Type:     framework.TypeInt64,
			Required: true,
			Description: `The lease duration (validity period of the
certificate) if no specific lease duration is requested.
The lease duration controls the expiration of certificates
issued by this backend. Defaults to the system default
value or the value of max_ttl, whichever is shorter.`,
		},

		"max_ttl": {
			Type:     framework.TypeInt64,
			Required: true,
			Description: `The maximum allowed lease duration. If not
set, defaults to the system maximum lease TTL.`,
		},
		"allow_token_displayname": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `Whether to allow "localhost" and "localdomain"
as a valid common name in a request, independent of allowed_domains value.`,
		},

		"allow_localhost": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `Whether to allow "localhost" and "localdomain"
as a valid common name in a request, independent of allowed_domains value.`,
		},

		"allowed_domains": {
			Type:     framework.TypeCommaStringSlice,
			Required: true,
			Description: `Specifies the domains this role is allowed
to issue certificates for. This is used with the allow_bare_domains,
allow_subdomains, and allow_glob_domains to determine matches for the
common name, DNS-typed SAN entries, and Email-typed SAN entries of
certificates. See the documentation for more information. This parameter
accepts a comma-separated string or list of domains.`,
		},
		"allowed_domains_template": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, Allowed domains can be specified using identity template policies.
				Non-templated domains are also permitted.`,
		},
		"allow_bare_domains": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, clients can request certificates
for the base domains themselves, e.g. "example.com" of domains listed
in allowed_domains. This is a separate option as in some cases this can
be considered a security threat. See the documentation for more
information.`,
		},

		"allow_subdomains": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, clients can request certificates for
subdomains of domains listed in allowed_domains, including wildcard
subdomains. See the documentation for more information.`,
		},

		"allow_glob_domains": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, domains specified in allowed_domains
can include shell-style glob patterns, e.g. "ftp*.example.com".
See the documentation for more information.`,
		},

		"allow_wildcard_certificates": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, allows certificates with wildcards in
the common name to be issued, conforming to RFC 6125's Section 6.4.3; e.g.,
"*.example.net" or "b*z.example.net". See the documentation for more
information.`,
		},

		"allow_any_name": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, clients can request certificates for
any domain, regardless of allowed_domains restrictions.
See the documentation for more information.`,
		},

		"enforce_hostnames": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, only valid host names are allowed for
CN and DNS SANs, and the host part of email addresses. Defaults to true.`,
		},

		"allow_ip_sans": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, IP Subject Alternative Names are allowed.
Any valid IP is accepted and No authorization checking is performed.`,
		},

		"allowed_uri_sans": {
			Type:     framework.TypeCommaStringSlice,
			Required: true,
			Description: `If set, an array of allowed URIs for URI Subject Alternative Names.
Any valid URI is accepted, these values support globbing.`,
		},

		"allowed_uri_sans_template": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, Allowed URI SANs can be specified using identity template policies.
				Non-templated URI SANs are also permitted.`,
		},

		"allowed_other_sans": {
			Type:        framework.TypeCommaStringSlice,
			Required:    true,
			Description: `If set, an array of allowed other names to put in SANs. These values support globbing and must be in the format <oid>;<type>:<value>. Currently only "utf8" is a valid type. All values, including globbing values, must use this syntax, with the exception being a single "*" which allows any OID and any value (but type must still be utf8).`,
		},

		"allowed_serial_numbers": {
			Type:        framework.TypeCommaStringSlice,
			Required:    true,
			Description: `If set, an array of allowed serial numbers to put in Subject. These values support globbing.`,
		},
		"allowed_user_ids": {
			Type:        framework.TypeCommaStringSlice,
			Description: `If set, an array of allowed user-ids to put in user system login name specified here: https://www.rfc-editor.org/rfc/rfc1274#section-9.3.1`,
		},
		"server_flag": {
			Type:    framework.TypeBool,
			Default: true,
			Description: `If set, certificates are flagged for server auth use.
Defaults to true. See also RFC 5280 Section 4.2.1.12.`,
		},

		"client_flag": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, certificates are flagged for client auth use.
Defaults to true. See also RFC 5280 Section 4.2.1.12.`,
		},

		"code_signing_flag": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, certificates are flagged for code signing
use. Defaults to false. See also RFC 5280 Section 4.2.1.12.`,
		},

		"email_protection_flag": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, certificates are flagged for email
protection use. Defaults to false. See also RFC 5280 Section 4.2.1.12.`,
		},

		"key_type": {
			Type:     framework.TypeString,
			Required: true,
			Description: `The type of key to use; defaults to RSA. "rsa"
"ec", "ed25519" and "any" are the only valid values.`,
		},

		"key_bits": {
			Type:     framework.TypeInt,
			Required: true,
			Description: `The number of bits to use. Allowed values are
0 (universal default); with rsa key_type: 2048 (default), 3072, or
4096; with ec key_type: 224, 256 (default), 384, or 521; ignored with
ed25519.`,
		},
		"signature_bits": {
			Type:     framework.TypeInt,
			Required: true,
			Description: `The number of bits to use in the signature
algorithm; accepts 256 for SHA-2-256, 384 for SHA-2-384, and 512 for
SHA-2-512. Defaults to 0 to automatically detect based on key length
(SHA-2-256 for RSA keys, and matching the curve size for NIST P-Curves).`,
		},
		"use_pss": {
			Type:     framework.TypeBool,
			Required: false,
			Description: `Whether or not to use PSS signatures when using a
RSA key-type issuer. Defaults to false.`,
		},
		"key_usage": {
			Type:     framework.TypeCommaStringSlice,
			Required: true,
			Description: `A comma-separated string or list of key usages (not extended
key usages). Valid values can be found at
https://golang.org/pkg/crypto/x509/#KeyUsage
-- simply drop the "KeyUsage" part of the name.
To remove all key usages from being set, set
this value to an empty list. See also RFC 5280
Section 4.2.1.3.`,
		},

		"ext_key_usage": {
			Type:     framework.TypeCommaStringSlice,
			Required: true,
			Description: `A comma-separated string or list of extended key usages. Valid values can be found at
https://golang.org/pkg/crypto/x509/#ExtKeyUsage
-- simply drop the "ExtKeyUsage" part of the name.
To remove all key usages from being set, set
this value to an empty list. See also RFC 5280
Section 4.2.1.12.`,
		},

		"ext_key_usage_oids": {
			Type:        framework.TypeCommaStringSlice,
			Required:    true,
			Description: `A comma-separated string or list of extended key usage oids.`,
		},

		"use_csr_common_name": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, when used with a signing profile,
the common name in the CSR will be used. This
does *not* include any requested Subject Alternative
Names; use use_csr_sans for that. Defaults to true.`,
		},

		"use_csr_sans": {
			Type:     framework.TypeBool,
			Required: true,
			Description: `If set, when used with a signing profile,
the SANs in the CSR will be used. This does *not*
include the Common Name (cn); use use_csr_common_name
for that. Defaults to true.`,
		},

		"ou": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, OU (OrganizationalUnit) will be set to
this value in certificates issued by this role.`,
		},

		"organization": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, O (Organization) will be set to
this value in certificates issued by this role.`,
		},

		"country": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, Country will be set to
this value in certificates issued by this role.`,
		},

		"locality": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, Locality will be set to
this value in certificates issued by this role.`,
		},

		"province": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, Province will be set to
this value in certificates issued by this role.`,
		},

		"street_address": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, Street Address will be set to
this value in certificates issued by this role.`,
		},

		"postal_code": {
			Type: framework.TypeCommaStringSlice,
			Description: `If set, Postal Code will be set to
this value in certificates issued by this role.`,
		},

		"generate_lease": {
			Type: framework.TypeBool,
			Description: `
If set, certificates issued/signed against this role will have OpenBao leases
attached to them. Defaults to "false". Certificates can be added to the CRL by
"bao revoke <lease_id>" when certificates are associated with leases.  It can
also be done using the "pki/revoke" endpoint. However, when lease generation is
disabled, invoking "pki/revoke" would be the only way to add the certificates
to the CRL.  When large number of certificates are generated with long
lifetimes, it is recommended that lease generation be disabled, as large amount of
leases adversely affect the startup time of OpenBao.`,
		},

		"no_store": {
			Type: framework.TypeBool,
			Description: `
If set, certificates issued/signed against this role will not be stored in the
storage backend. This can improve performance when issuing large numbers of 
certificates. However, certificates issued in this way cannot be enumerated
or revoked, so this option is recommended only for certificates that are
non-sensitive, or extremely short-lived. This option implies a value of "false"
for "generate_lease".`,
		},

		"require_cn": {
			Type:        framework.TypeBool,
			Description: `If set to false, makes the 'common_name' field optional while generating a certificate.`,
		},

		"cn_validations": {
			Type: framework.TypeCommaStringSlice,
			Description: `List of allowed validations to run against the
Common Name field. Values can include 'email' to validate the CN is a email
address, 'hostname' to validate the CN is a valid hostname (potentially
including wildcards). When multiple validations are specified, these take
OR semantics (either email OR hostname are allowed). The special value
'disabled' allows disabling all CN name validations, allowing for arbitrary
non-Hostname, non-Email address CNs.`,
		},

		"policy_identifiers": {
			Type: framework.TypeCommaStringSlice,
			Description: `A comma-separated string or list of policy OIDs, or a JSON list of qualified policy
information, which must include an oid, and may include a notice and/or cps url, using the form 
[{"oid"="1.3.6.1.4.1.7.8","notice"="I am a user Notice"}, {"oid"="1.3.6.1.4.1.44947.1.2.4 ","cps"="https://example.com"}].`,
		},

		"basic_constraints_valid_for_non_ca": {
			Type:        framework.TypeBool,
			Description: `Mark Basic Constraints valid when issuing non-CA certificates.`,
		},
		"not_before_duration": {
			Type:        framework.TypeInt64,
			Description: `The duration in seconds before now which the certificate needs to be backdated by.`,
		},
		"not_before": {
			Type: framework.TypeString,
			Description: `Set the not before field of the certificate with specified date value.
The value format should be given in UTC format YYYY-MM-ddTHH:MM:SSZ.`,
		},
		"not_after": {
			Type: framework.TypeString,
			Description: `Set the not after field of the certificate with specified date value.
The value format should be given in UTC format YYYY-MM-ddTHH:MM:SSZ.`,
		},
		"issuer_ref": {
			Type: framework.TypeString,
			Description: `Reference to the issuer used to sign requests
serviced by this role.`,
		},
	}

	return &framework.Path{
		Pattern: "roles/" + framework.GenericNameRegex("name"),

		DisplayAttrs: &framework.DisplayAttributes{
			OperationPrefix: operationPrefixPKI,
			OperationSuffix: "role",
		},

		Fields: map[string]*framework.FieldSchema{
			"backend": {
				Type:        framework.TypeString,
				Description: "Backend Type",
			},

			"name": {
				Type:        framework.TypeString,
				Description: "Name of the role",
			},

			"ttl": {
				Type: framework.TypeDurationSecond,
				Description: `The lease duration (validity period of the
certificate) if no specific lease duration is requested.
The lease duration controls the expiration of certificates
issued by this backend. Defaults to the system default
value or the value of max_ttl, whichever is shorter.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "TTL",
				},
			},

			"max_ttl": {
				Type: framework.TypeDurationSecond,
				Description: `The maximum allowed lease duration. If not
set, defaults to the system maximum lease TTL.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Max TTL",
				},
			},

			"allow_localhost": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `Whether to allow "localhost" and "localdomain"
as a valid common name in a request, independent of allowed_domains value.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: true,
				},
			},

			"allowed_domains": {
				Type: framework.TypeCommaStringSlice,
				Description: `Specifies the domains this role is allowed
to issue certificates for. This is used with the allow_bare_domains,
allow_subdomains, and allow_glob_domains to determine matches for the
common name, DNS-typed SAN entries, and Email-typed SAN entries of
certificates. See the documentation for more information. This parameter
accepts a comma-separated string or list of domains.`,
			},
			"allowed_domains_template": {
				Type: framework.TypeBool,
				Description: `If set, Allowed domains can be specified using identity template policies.
				Non-templated domains are also permitted.`,
				Default: false,
			},
			"allow_bare_domains": {
				Type: framework.TypeBool,
				Description: `If set, clients can request certificates
for the base domains themselves, e.g. "example.com" of domains listed
in allowed_domains. This is a separate option as in some cases this can
be considered a security threat. See the documentation for more
information.`,
			},

			"allow_subdomains": {
				Type: framework.TypeBool,
				Description: `If set, clients can request certificates for
subdomains of domains listed in allowed_domains, including wildcard
subdomains. See the documentation for more information.`,
			},

			"allow_glob_domains": {
				Type: framework.TypeBool,
				Description: `If set, domains specified in allowed_domains
can include shell-style glob patterns, e.g. "ftp*.example.com".
See the documentation for more information.`,
			},

			"allow_wildcard_certificates": {
				Type: framework.TypeBool,
				Description: `If set, allows certificates with wildcards in
the common name to be issued, conforming to RFC 6125's Section 6.4.3; e.g.,
"*.example.net" or "b*z.example.net". See the documentation for more
information.`,
				Default: true,
			},

			"allow_any_name": {
				Type: framework.TypeBool,
				Description: `If set, clients can request certificates for
any domain, regardless of allowed_domains restrictions.
See the documentation for more information.`,
			},

			"enforce_hostnames": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, only valid host names are allowed for
CN and DNS SANs, and the host part of email addresses. Defaults to true.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: true,
				},
			},

			"allow_ip_sans": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, IP Subject Alternative Names are allowed.
Any valid IP is accepted and No authorization checking is performed.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name:  "Allow IP Subject Alternative Names",
					Value: true,
				},
			},

			"allowed_uri_sans": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, an array of allowed URIs for URI Subject Alternative Names.
Any valid URI is accepted, these values support globbing.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Allowed URI Subject Alternative Names",
				},
			},

			"allowed_uri_sans_template": {
				Type: framework.TypeBool,
				Description: `If set, Allowed URI SANs can be specified using identity template policies.
				Non-templated URI SANs are also permitted.`,
				Default: false,
			},

			"allowed_other_sans": {
				Type:        framework.TypeCommaStringSlice,
				Description: `If set, an array of allowed other names to put in SANs. These values support globbing and must be in the format <oid>;<type>:<value>. Currently only "utf8" is a valid type. All values, including globbing values, must use this syntax, with the exception being a single "*" which allows any OID and any value (but type must still be utf8).`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Allowed Other Subject Alternative Names",
				},
			},

			"allowed_serial_numbers": {
				Type:        framework.TypeCommaStringSlice,
				Description: `If set, an array of allowed serial numbers to put in Subject. These values support globbing.`,
			},

			"allowed_user_ids": {
				Type:        framework.TypeCommaStringSlice,
				Description: `If set, an array of allowed user-ids to put in user system login name specified here: https://www.rfc-editor.org/rfc/rfc1274#section-9.3.1`,
			},

			"server_flag": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, certificates are flagged for server auth use.
Defaults to true. See also RFC 5280 Section 4.2.1.12.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: true,
				},
			},

			"client_flag": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, certificates are flagged for client auth use.
Defaults to true. See also RFC 5280 Section 4.2.1.12.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: true,
				},
			},

			"code_signing_flag": {
				Type: framework.TypeBool,
				Description: `If set, certificates are flagged for code signing
use. Defaults to false. See also RFC 5280 Section 4.2.1.12.`,
			},

			"email_protection_flag": {
				Type: framework.TypeBool,
				Description: `If set, certificates are flagged for email
protection use. Defaults to false. See also RFC 5280 Section 4.2.1.12.`,
			},

			"key_type": {
				Type:    framework.TypeString,
				Default: "rsa",
				Description: `The type of key to use; defaults to RSA. "rsa"
"ec", "ed25519" and "any" are the only valid values.`,
				AllowedValues: []interface{}{"rsa", "ec", "ed25519", "any"},
			},

			"key_bits": {
				Type:    framework.TypeInt,
				Default: 0,
				Description: `The number of bits to use. Allowed values are
0 (universal default); with rsa key_type: 2048 (default), 3072, or
4096; with ec key_type: 224, 256 (default), 384, or 521; ignored with
ed25519.`,
			},

			"signature_bits": {
				Type:    framework.TypeInt,
				Default: 0,
				Description: `The number of bits to use in the signature
algorithm; accepts 256 for SHA-2-256, 384 for SHA-2-384, and 512 for
SHA-2-512. Defaults to 0 to automatically detect based on key length
(SHA-2-256 for RSA keys, and matching the curve size for NIST P-Curves).`,
			},

			"use_pss": {
				Type:    framework.TypeBool,
				Default: false,
				Description: `Whether or not to use PSS signatures when using a
RSA key-type issuer. Defaults to false.`,
			},

			"key_usage": {
				Type:    framework.TypeCommaStringSlice,
				Default: []string{"DigitalSignature", "KeyAgreement", "KeyEncipherment"},
				Description: `A comma-separated string or list of key usages (not extended
key usages). Valid values can be found at
https://golang.org/pkg/crypto/x509/#KeyUsage
-- simply drop the "KeyUsage" part of the name.
To remove all key usages from being set, set
this value to an empty list. See also RFC 5280
Section 4.2.1.3.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: "DigitalSignature,KeyAgreement,KeyEncipherment",
				},
			},

			"ext_key_usage": {
				Type:    framework.TypeCommaStringSlice,
				Default: []string{},
				Description: `A comma-separated string or list of extended key usages. Valid values can be found at
https://golang.org/pkg/crypto/x509/#ExtKeyUsage
-- simply drop the "ExtKeyUsage" part of the name.
To remove all key usages from being set, set
this value to an empty list. See also RFC 5280
Section 4.2.1.12.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Extended Key Usage",
				},
			},

			"ext_key_usage_oids": {
				Type:        framework.TypeCommaStringSlice,
				Description: `A comma-separated string or list of extended key usage oids.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Extended Key Usage OIDs",
				},
			},

			"use_csr_common_name": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, when used with a signing profile,
the common name in the CSR will be used. This
does *not* include any requested Subject Alternative
Names; use use_csr_sans for that. Defaults to true.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name:  "Use CSR Common Name",
					Value: true,
				},
			},

			"use_csr_sans": {
				Type:    framework.TypeBool,
				Default: true,
				Description: `If set, when used with a signing profile,
the SANs in the CSR will be used. This does *not*
include the Common Name (cn); use use_csr_common_name
for that. Defaults to true.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name:  "Use CSR Subject Alternative Names",
					Value: true,
				},
			},

			"ou": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, OU (OrganizationalUnit) will be set to
this value in certificates issued by this role.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Organizational Unit",
				},
			},

			"organization": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, O (Organization) will be set to
this value in certificates issued by this role.`,
			},

			"country": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, Country will be set to
this value in certificates issued by this role.`,
			},

			"locality": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, Locality will be set to
this value in certificates issued by this role.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Locality/City",
				},
			},

			"province": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, Province will be set to
this value in certificates issued by this role.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Province/State",
				},
			},

			"street_address": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, Street Address will be set to
this value in certificates issued by this role.`,
			},

			"postal_code": {
				Type: framework.TypeCommaStringSlice,
				Description: `If set, Postal Code will be set to
this value in certificates issued by this role.`,
			},

			"generate_lease": {
				Type: framework.TypeBool,
				Description: `
If set, certificates issued/signed against this role will have OpenBao leases
attached to them. Defaults to "false". Certificates can be added to the CRL by
"bao revoke <lease_id>" when certificates are associated with leases.  It can
also be done using the "pki/revoke" endpoint. However, when lease generation is
disabled, invoking "pki/revoke" would be the only way to add the certificates
to the CRL.  When large number of certificates are generated with long
lifetimes, it is recommended that lease generation be disabled, as large amount of
leases adversely affect the startup time of OpenBao.`,
			},

			"no_store": {
				Type: framework.TypeBool,
				Description: `
If set, certificates issued/signed against this role will not be stored in the
storage backend. This can improve performance when issuing large numbers of 
certificates. However, certificates issued in this way cannot be enumerated
or revoked, so this option is recommended only for certificates that are
non-sensitive, or extremely short-lived. This option implies a value of "false"
for "generate_lease".`,
			},

			"require_cn": {
				Type:        framework.TypeBool,
				Default:     true,
				Description: `If set to false, makes the 'common_name' field optional while generating a certificate.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Require Common Name",
				},
			},

			"cn_validations": {
				Type:    framework.TypeCommaStringSlice,
				Default: []string{"email", "hostname"},
				Description: `List of allowed validations to run against the
Common Name field. Values can include 'email' to validate the CN is a email
address, 'hostname' to validate the CN is a valid hostname (potentially
including wildcards). When multiple validations are specified, these take
OR semantics (either email OR hostname are allowed). The special value
'disabled' allows disabling all CN name validations, allowing for arbitrary
non-Hostname, non-Email address CNs.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Common Name Validations",
				},
			},

			"policy_identifiers": {
				Type: framework.TypeCommaStringSlice,
				Description: `A comma-separated string or list of policy OIDs, or a JSON list of qualified policy
information, which must include an oid, and may include a notice and/or cps url, using the form 
[{"oid"="1.3.6.1.4.1.7.8","notice"="I am a user Notice"}, {"oid"="1.3.6.1.4.1.44947.1.2.4 ","cps"="https://example.com"}].`,
			},

			"basic_constraints_valid_for_non_ca": {
				Type:        framework.TypeBool,
				Description: `Mark Basic Constraints valid when issuing non-CA certificates.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Name: "Basic Constraints Valid for Non-CA",
				},
			},
			"not_before_duration": {
				Type:        framework.TypeDurationSecond,
				Default:     30,
				Description: `The duration before now which the certificate needs to be backdated by.`,
				DisplayAttrs: &framework.DisplayAttributes{
					Value: 30,
				},
			},
			"not_before": {
				Type: framework.TypeString,
				Description: `Set the not before field of the certificate with specified date value.
The value format should be given in UTC format YYYY-MM-ddTHH:MM:SSZ.`,
			},
			"not_after": {
				Type: framework.TypeString,
				Description: `Set the not after field of the certificate with specified date value.
The value format should be given in UTC format YYYY-MM-ddTHH:MM:SSZ.`,
			},
			"issuer_ref": {
				Type: framework.TypeString,
				Description: `Reference to the issuer used to sign requests
serviced by this role.`,
				Default: defaultRef,
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathRoleRead,
				Responses: map[int][]framework.Response{
					http.StatusOK: {{
						Description: "OK",
						Fields:      pathRolesResponseFields,
					}},
				},
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathRoleCreate,
				Responses: map[int][]framework.Response{
					http.StatusOK: {{
						Description: "OK",
						Fields:      pathRolesResponseFields,
					}},
				},
				// Read more about why these flags are set in backend.go.
				ForwardPerformanceStandby:   true,
				ForwardPerformanceSecondary: true,
			},
			logical.DeleteOperation: &framework.PathOperation{
				Callback: b.pathRoleDelete,
				Responses: map[int][]framework.Response{
					http.StatusNoContent: {{
						Description: "No Content",
					}},
				},
				// Read more about why these flags are set in backend.go.
				ForwardPerformanceStandby:   true,
				ForwardPerformanceSecondary: true,
			},
			logical.PatchOperation: &framework.PathOperation{
				Callback: b.pathRolePatch,
				Responses: map[int][]framework.Response{
					http.StatusOK: {{
						Description: "OK",
						Fields:      pathRolesResponseFields,
					}},
				},
				// Read more about why these flags are set in backend.go.
				ForwardPerformanceStandby:   true,
				ForwardPerformanceSecondary: true,
			},
		},

		HelpSynopsis:    pathRoleHelpSyn,
		HelpDescription: pathRoleHelpDesc,
	}
}

func (b *backend) getRole(ctx context.Context, s logical.Storage, n string) (*roleEntry, error) {
	entry, err := s.Get(ctx, "role/"+n)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	var result roleEntry
	if err := entry.DecodeJSON(&result); err != nil {
		return nil, err
	}

	// Migrate existing saved entries and save back if changed
	modified := false
	if len(result.DeprecatedTTL) == 0 && len(result.Lease) != 0 {
		result.DeprecatedTTL = result.Lease
		result.Lease = ""
		modified = true
	}
	if result.TTL == 0 && len(result.DeprecatedTTL) != 0 {
		parsed, err := parseutil.ParseDurationSecond(result.DeprecatedTTL)
		if err != nil {
			return nil, err
		}
		result.TTL = parsed
		result.DeprecatedTTL = ""
		modified = true
	}
	if len(result.DeprecatedMaxTTL) == 0 && len(result.LeaseMax) != 0 {
		result.DeprecatedMaxTTL = result.LeaseMax
		result.LeaseMax = ""
		modified = true
	}
	if result.MaxTTL == 0 && len(result.DeprecatedMaxTTL) != 0 {
		parsed, err := parseutil.ParseDurationSecond(result.DeprecatedMaxTTL)
		if err != nil {
			return nil, err
		}
		result.MaxTTL = parsed
		result.DeprecatedMaxTTL = ""
		modified = true
	}
	if result.AllowBaseDomain {
		result.AllowBaseDomain = false
		result.AllowBareDomains = true
		modified = true
	}
	if result.AllowedDomainsOld != "" {
		result.AllowedDomains = strings.Split(result.AllowedDomainsOld, ",")
		result.AllowedDomainsOld = ""
		modified = true
	}
	if result.AllowedBaseDomain != "" {
		found := false
		for _, v := range result.AllowedDomains {
			if v == result.AllowedBaseDomain {
				found = true
				break
			}
		}
		if !found {
			result.AllowedDomains = append(result.AllowedDomains, result.AllowedBaseDomain)
		}
		result.AllowedBaseDomain = ""
		modified = true
	}
	if result.AllowWildcardCertificates == nil {
		// While not the most secure default, when AllowWildcardCertificates isn't
		// explicitly specified in the stored Role, we automatically upgrade it to
		// true to preserve compatibility with previous versions of Vault. Once this
		// field is set, this logic will not be triggered any more.
		result.AllowWildcardCertificates = new(bool)
		*result.AllowWildcardCertificates = true
		modified = true
	}

	// Upgrade generate_lease in role
	if result.GenerateLease == nil {
		// All the new roles will have GenerateLease always set to a value. A
		// nil value indicates that this role needs an upgrade. Set it to
		// `true` to not alter its current behavior.
		result.GenerateLease = new(bool)
		*result.GenerateLease = true
		modified = true
	}

	// Upgrade key usages
	if result.KeyUsageOld != "" {
		result.KeyUsage = strings.Split(result.KeyUsageOld, ",")
		result.KeyUsageOld = ""
		modified = true
	}

	// Upgrade OU
	if result.OUOld != "" {
		result.OU = strings.Split(result.OUOld, ",")
		result.OUOld = ""
		modified = true
	}

	// Upgrade Organization
	if result.OrganizationOld != "" {
		result.Organization = strings.Split(result.OrganizationOld, ",")
		result.OrganizationOld = ""
		modified = true
	}

	// Set the issuer field to default if not set. We want to do this
	// unconditionally as we should probably never have an empty issuer
	// on a stored roles.
	if len(result.Issuer) == 0 {
		result.Issuer = defaultRef
		modified = true
	}

	// Update CN Validations to be the present default, "email,hostname"
	if len(result.CNValidations) == 0 {
		result.CNValidations = []string{"email", "hostname"}
		modified = true
	}

	// Ensure the role is valid after updating.
	_, err = validateRole(b, &result, ctx, s)
	if err != nil {
		return nil, err
	}

	if modified && (b.System().LocalMount() || !b.System().ReplicationState().HasState(consts.ReplicationPerformanceSecondary)) {
		jsonEntry, err := logical.StorageEntryJSON("role/"+n, &result)
		if err != nil {
			return nil, err
		}
		if err := s.Put(ctx, jsonEntry); err != nil {
			// Only perform upgrades on replication primary
			if !strings.Contains(err.Error(), logical.ErrReadOnly.Error()) {
				return nil, err
			}
		}
	}

	result.Name = n

	return &result, nil
}

func (b *backend) pathRoleDelete(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	err := req.Storage.Delete(ctx, "role/"+data.Get("name").(string))
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (b *backend) pathRoleRead(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	roleName := data.Get("name").(string)
	if roleName == "" {
		return logical.ErrorResponse("missing role name"), nil
	}

	role, err := b.getRole(ctx, req.Storage, roleName)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, nil
	}

	resp := &logical.Response{
		Data: role.ToResponseData(),
	}
	return resp, nil
}

func (b *backend) pathRoleList(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	after := data.Get("after").(string)
	limit := data.Get("limit").(int)

	entries, err := req.Storage.ListPage(ctx, "role/", after, limit)
	if err != nil {
		return nil, err
	}

	return logical.ListResponse(entries), nil
}

func (b *backend) pathRoleCreate(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	var err error
	name := data.Get("name").(string)

	entry := &roleEntry{
		MaxTTL:                        time.Duration(data.Get("max_ttl").(int)) * time.Second,
		TTL:                           time.Duration(data.Get("ttl").(int)) * time.Second,
		AllowLocalhost:                data.Get("allow_localhost").(bool),
		AllowedDomains:                data.Get("allowed_domains").([]string),
		AllowedDomainsTemplate:        data.Get("allowed_domains_template").(bool),
		AllowBareDomains:              data.Get("allow_bare_domains").(bool),
		AllowSubdomains:               data.Get("allow_subdomains").(bool),
		AllowGlobDomains:              data.Get("allow_glob_domains").(bool),
		AllowWildcardCertificates:     new(bool), // Handled specially below
		AllowAnyName:                  data.Get("allow_any_name").(bool),
		AllowedURISANsTemplate:        data.Get("allowed_uri_sans_template").(bool),
		EnforceHostnames:              data.Get("enforce_hostnames").(bool),
		AllowIPSANs:                   data.Get("allow_ip_sans").(bool),
		AllowedURISANs:                data.Get("allowed_uri_sans").([]string),
		ServerFlag:                    data.Get("server_flag").(bool),
		ClientFlag:                    data.Get("client_flag").(bool),
		CodeSigningFlag:               data.Get("code_signing_flag").(bool),
		EmailProtectionFlag:           data.Get("email_protection_flag").(bool),
		KeyType:                       data.Get("key_type").(string),
		KeyBits:                       data.Get("key_bits").(int),
		SignatureBits:                 data.Get("signature_bits").(int),
		UsePSS:                        data.Get("use_pss").(bool),
		UseCSRCommonName:              data.Get("use_csr_common_name").(bool),
		UseCSRSANs:                    data.Get("use_csr_sans").(bool),
		KeyUsage:                      data.Get("key_usage").([]string),
		ExtKeyUsage:                   data.Get("ext_key_usage").([]string),
		ExtKeyUsageOIDs:               data.Get("ext_key_usage_oids").([]string),
		OU:                            data.Get("ou").([]string),
		Organization:                  data.Get("organization").([]string),
		Country:                       data.Get("country").([]string),
		Locality:                      data.Get("locality").([]string),
		Province:                      data.Get("province").([]string),
		StreetAddress:                 data.Get("street_address").([]string),
		PostalCode:                    data.Get("postal_code").([]string),
		GenerateLease:                 new(bool),
		NoStore:                       data.Get("no_store").(bool),
		RequireCN:                     data.Get("require_cn").(bool),
		CNValidations:                 data.Get("cn_validations").([]string),
		AllowedSerialNumbers:          data.Get("allowed_serial_numbers").([]string),
		AllowedUserIDs:                data.Get("allowed_user_ids").([]string),
		PolicyIdentifiers:             getPolicyIdentifier(data, nil),
		BasicConstraintsValidForNonCA: data.Get("basic_constraints_valid_for_non_ca").(bool),
		NotBeforeDuration:             time.Duration(data.Get("not_before_duration").(int)) * time.Second,
		NotBefore:                     data.Get("not_before").(string),
		NotAfter:                      data.Get("not_after").(string),
		Issuer:                        data.Get("issuer_ref").(string),
		Name:                          name,
	}

	allowedOtherSANs := data.Get("allowed_other_sans").([]string)
	switch {
	case len(allowedOtherSANs) == 0:
	case len(allowedOtherSANs) == 1 && allowedOtherSANs[0] == "*":
	default:
		_, err := parseOtherSANs(allowedOtherSANs)
		if err != nil {
			return logical.ErrorResponse(fmt.Errorf("error parsing allowed_other_sans: %w", err).Error()), nil
		}
	}
	entry.AllowedOtherSANs = allowedOtherSANs

	allowWildcardCertificates, present := data.GetOk("allow_wildcard_certificates")
	if !present {
		// While not the most secure default, when AllowWildcardCertificates isn't
		// explicitly specified in the request, we automatically set it to true to
		// preserve compatibility with previous versions of Vault.
		allowWildcardCertificates = true
	}
	*entry.AllowWildcardCertificates = allowWildcardCertificates.(bool)

	warning := ""
	// no_store implies generate_lease := false
	if entry.NoStore {
		*entry.GenerateLease = false
		if data.Get("generate_lease").(bool) {
			warning = "mutually exclusive values no_store=true and generate_lease=true were both specified; no_store=true takes priority"
		}
	} else {
		*entry.GenerateLease = data.Get("generate_lease").(bool)
		if *entry.GenerateLease {
			warning = "it is encouraged to disable generate_lease and rely on PKI's native capabilities when possible; this option can cause instance-wide issues with large numbers of issued certificates"
		}
	}

	resp, err := validateRole(b, entry, ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		resp.AddWarning(warning)
	}
	if resp.IsError() {
		return resp, nil
	}

	// Store it
	jsonEntry, err := logical.StorageEntryJSON("role/"+name, entry)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, jsonEntry); err != nil {
		return nil, err
	}

	return resp, nil
}

func validateRole(b *backend, entry *roleEntry, ctx context.Context, s logical.Storage) (*logical.Response, error) {
	resp := &logical.Response{}
	var err error

	if entry.MaxTTL > 0 && entry.TTL > entry.MaxTTL {
		return logical.ErrorResponse(
			`"ttl" value must be less than "max_ttl" value`,
		), nil
	}

	if entry.KeyBits, entry.SignatureBits, err = certutil.ValidateDefaultOrValueKeyTypeSignatureLength(entry.KeyType, entry.KeyBits, entry.SignatureBits); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}

	if len(entry.ExtKeyUsageOIDs) > 0 {
		for _, oidstr := range entry.ExtKeyUsageOIDs {
			_, err := certutil.StringToOid(oidstr)
			if err != nil {
				return logical.ErrorResponse(fmt.Sprintf("%q could not be parsed as a valid oid for an extended key usage", oidstr)), nil
			}
		}
	}

	if len(entry.PolicyIdentifiers) > 0 {
		_, err := certutil.CreatePolicyInformationExtensionFromStorageStrings(entry.PolicyIdentifiers)
		if err != nil {
			return nil, err
		}
	}

	// Ensure issuers ref is set to a non-empty value. Note that we never
	// resolve the reference (to an issuerId) at role creation time; instead,
	// resolve it at use time. This allows values such as `default` or other
	// user-assigned names to "float" and change over time.
	if len(entry.Issuer) == 0 {
		entry.Issuer = defaultRef
	}
	// Check that the issuers reference set resolves to something
	if !b.useLegacyBundleCaStorage() {
		sc := b.makeStorageContext(ctx, s)
		issuerId, err := sc.resolveIssuerReference(entry.Issuer)
		if err != nil {
			if issuerId == IssuerRefNotFound {
				resp = &logical.Response{}
				if entry.Issuer == defaultRef {
					resp.AddWarning("Issuing Certificate was set to default, but no default issuing certificate (configurable at /config/issuers) is currently set")
				} else {
					resp.AddWarning(fmt.Sprintf("Issuing Certificate was set to %s but no issuing certificate currently has that name", entry.Issuer))
				}
			} else {
				return nil, err
			}
		}

	}

	// Ensures CNValidations are alright
	entry.CNValidations, err = checkCNValidations(entry.CNValidations)
	if err != nil {
		return nil, errutil.UserError{Err: err.Error()}
	}

	resp.Data = entry.ToResponseData()
	return resp, nil
}

func getWithExplicitDefault(data *framework.FieldData, field string, defaultValue interface{}) interface{} {
	assignedValue, ok := data.GetOk(field)
	if ok {
		return assignedValue
	}
	return defaultValue
}

func getTimeWithExplicitDefault(data *framework.FieldData, field string, defaultValue time.Duration) time.Duration {
	assignedValue, ok := data.GetOk(field)
	if ok {
		return time.Duration(assignedValue.(int)) * time.Second
	}
	return defaultValue
}

func (b *backend) pathRolePatch(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	name := data.Get("name").(string)

	oldEntry, err := b.getRole(ctx, req.Storage, name)
	if err != nil {
		return nil, err
	}
	if oldEntry == nil {
		return logical.ErrorResponse("Unable to fetch role entry to patch"), nil
	}

	entry := &roleEntry{
		MaxTTL:                        getTimeWithExplicitDefault(data, "max_ttl", oldEntry.MaxTTL),
		TTL:                           getTimeWithExplicitDefault(data, "ttl", oldEntry.TTL),
		AllowLocalhost:                getWithExplicitDefault(data, "allow_localhost", oldEntry.AllowLocalhost).(bool),
		AllowedDomains:                getWithExplicitDefault(data, "allowed_domains", oldEntry.AllowedDomains).([]string),
		AllowedDomainsTemplate:        getWithExplicitDefault(data, "allowed_domains_template", oldEntry.AllowedDomainsTemplate).(bool),
		AllowBareDomains:              getWithExplicitDefault(data, "allow_bare_domains", oldEntry.AllowBareDomains).(bool),
		AllowSubdomains:               getWithExplicitDefault(data, "allow_subdomains", oldEntry.AllowSubdomains).(bool),
		AllowGlobDomains:              getWithExplicitDefault(data, "allow_glob_domains", oldEntry.AllowGlobDomains).(bool),
		AllowWildcardCertificates:     new(bool), // Handled specially below
		AllowAnyName:                  getWithExplicitDefault(data, "allow_any_name", oldEntry.AllowAnyName).(bool),
		AllowedURISANsTemplate:        getWithExplicitDefault(data, "allowed_uri_sans_template", oldEntry.AllowedURISANsTemplate).(bool),
		EnforceHostnames:              getWithExplicitDefault(data, "enforce_hostnames", oldEntry.EnforceHostnames).(bool),
		AllowIPSANs:                   getWithExplicitDefault(data, "allow_ip_sans", oldEntry.AllowIPSANs).(bool),
		AllowedURISANs:                getWithExplicitDefault(data, "allowed_uri_sans", oldEntry.AllowedURISANs).([]string),
		ServerFlag:                    getWithExplicitDefault(data, "server_flag", oldEntry.ServerFlag).(bool),
		ClientFlag:                    getWithExplicitDefault(data, "client_flag", oldEntry.ClientFlag).(bool),
		CodeSigningFlag:               getWithExplicitDefault(data, "code_signing_flag", oldEntry.CodeSigningFlag).(bool),
		EmailProtectionFlag:           getWithExplicitDefault(data, "email_protection_flag", oldEntry.EmailProtectionFlag).(bool),
		KeyType:                       getWithExplicitDefault(data, "key_type", oldEntry.KeyType).(string),
		KeyBits:                       getWithExplicitDefault(data, "key_bits", oldEntry.KeyBits).(int),
		SignatureBits:                 getWithExplicitDefault(data, "signature_bits", oldEntry.SignatureBits).(int),
		UsePSS:                        getWithExplicitDefault(data, "use_pss", oldEntry.UsePSS).(bool),
		UseCSRCommonName:              getWithExplicitDefault(data, "use_csr_common_name", oldEntry.UseCSRCommonName).(bool),
		UseCSRSANs:                    getWithExplicitDefault(data, "use_csr_sans", oldEntry.UseCSRSANs).(bool),
		KeyUsage:                      getWithExplicitDefault(data, "key_usage", oldEntry.KeyUsage).([]string),
		ExtKeyUsage:                   getWithExplicitDefault(data, "ext_key_usage", oldEntry.ExtKeyUsage).([]string),
		ExtKeyUsageOIDs:               getWithExplicitDefault(data, "ext_key_usage_oids", oldEntry.ExtKeyUsageOIDs).([]string),
		OU:                            getWithExplicitDefault(data, "ou", oldEntry.OU).([]string),
		Organization:                  getWithExplicitDefault(data, "organization", oldEntry.Organization).([]string),
		Country:                       getWithExplicitDefault(data, "country", oldEntry.Country).([]string),
		Locality:                      getWithExplicitDefault(data, "locality", oldEntry.Locality).([]string),
		Province:                      getWithExplicitDefault(data, "province", oldEntry.Province).([]string),
		StreetAddress:                 getWithExplicitDefault(data, "street_address", oldEntry.StreetAddress).([]string),
		PostalCode:                    getWithExplicitDefault(data, "postal_code", oldEntry.PostalCode).([]string),
		GenerateLease:                 new(bool),
		NoStore:                       getWithExplicitDefault(data, "no_store", oldEntry.NoStore).(bool),
		RequireCN:                     getWithExplicitDefault(data, "require_cn", oldEntry.RequireCN).(bool),
		CNValidations:                 getWithExplicitDefault(data, "cn_validations", oldEntry.CNValidations).([]string),
		AllowedSerialNumbers:          getWithExplicitDefault(data, "allowed_serial_numbers", oldEntry.AllowedSerialNumbers).([]string),
		AllowedUserIDs:                getWithExplicitDefault(data, "allowed_user_ids", oldEntry.AllowedUserIDs).([]string),
		PolicyIdentifiers:             getPolicyIdentifier(data, &oldEntry.PolicyIdentifiers),
		BasicConstraintsValidForNonCA: getWithExplicitDefault(data, "basic_constraints_valid_for_non_ca", oldEntry.BasicConstraintsValidForNonCA).(bool),
		NotBeforeDuration:             getTimeWithExplicitDefault(data, "not_before_duration", oldEntry.NotBeforeDuration),
		NotBefore:                     data.Get("not_before").(string),
		NotAfter:                      getWithExplicitDefault(data, "not_after", oldEntry.NotAfter).(string),
		Issuer:                        getWithExplicitDefault(data, "issuer_ref", oldEntry.Issuer).(string),
	}

	allowedOtherSANsData, wasSet := data.GetOk("allowed_other_sans")
	if wasSet {
		allowedOtherSANs := allowedOtherSANsData.([]string)
		switch {
		case len(allowedOtherSANs) == 0:
		case len(allowedOtherSANs) == 1 && allowedOtherSANs[0] == "*":
		default:
			_, err := parseOtherSANs(allowedOtherSANs)
			if err != nil {
				return logical.ErrorResponse(fmt.Errorf("error parsing allowed_other_sans: %w", err).Error()), nil
			}
		}
		entry.AllowedOtherSANs = allowedOtherSANs
	} else {
		entry.AllowedOtherSANs = oldEntry.AllowedOtherSANs
	}

	allowWildcardCertificates, present := data.GetOk("allow_wildcard_certificates")
	if !present {
		allowWildcardCertificates = *oldEntry.AllowWildcardCertificates
	}
	*entry.AllowWildcardCertificates = allowWildcardCertificates.(bool)

	warning := ""
	generateLease, ok := data.GetOk("generate_lease")
	// no_store implies generate_lease := false
	if entry.NoStore {
		*entry.GenerateLease = false
		if ok && generateLease.(bool) || !ok && *oldEntry.GenerateLease {
			warning = "mutually exclusive values no_store=true and generate_lease=true were both specified; no_store=true takes priority"
		}
	} else {
		if ok {
			*entry.GenerateLease = data.Get("generate_lease").(bool)
		} else {
			entry.GenerateLease = oldEntry.GenerateLease
		}

		if *entry.GenerateLease {
			warning = "it is encouraged to disable generate_lease and rely on PKI's native capabilities when possible; this option can cause instance-wide issues with large numbers of issued certificates"
		}
	}

	resp, err := validateRole(b, entry, ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		resp.AddWarning(warning)
	}
	if resp.IsError() {
		return resp, nil
	}

	// Store it
	jsonEntry, err := logical.StorageEntryJSON("role/"+name, entry)
	if err != nil {
		return nil, err
	}
	if err := req.Storage.Put(ctx, jsonEntry); err != nil {
		return nil, err
	}

	return resp, nil
}

func parseKeyUsages(input []string) int {
	var parsedKeyUsages x509.KeyUsage
	for _, k := range input {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "digitalsignature":
			parsedKeyUsages |= x509.KeyUsageDigitalSignature
		case "contentcommitment":
			parsedKeyUsages |= x509.KeyUsageContentCommitment
		case "keyencipherment":
			parsedKeyUsages |= x509.KeyUsageKeyEncipherment
		case "dataencipherment":
			parsedKeyUsages |= x509.KeyUsageDataEncipherment
		case "keyagreement":
			parsedKeyUsages |= x509.KeyUsageKeyAgreement
		case "certsign":
			parsedKeyUsages |= x509.KeyUsageCertSign
		case "crlsign":
			parsedKeyUsages |= x509.KeyUsageCRLSign
		case "encipheronly":
			parsedKeyUsages |= x509.KeyUsageEncipherOnly
		case "decipheronly":
			parsedKeyUsages |= x509.KeyUsageDecipherOnly
		}
	}

	return int(parsedKeyUsages)
}

func parseExtKeyUsages(role *roleEntry) certutil.CertExtKeyUsage {
	var parsedKeyUsages certutil.CertExtKeyUsage

	if role.ServerFlag {
		parsedKeyUsages |= certutil.ServerAuthExtKeyUsage
	}

	if role.ClientFlag {
		parsedKeyUsages |= certutil.ClientAuthExtKeyUsage
	}

	if role.CodeSigningFlag {
		parsedKeyUsages |= certutil.CodeSigningExtKeyUsage
	}

	if role.EmailProtectionFlag {
		parsedKeyUsages |= certutil.EmailProtectionExtKeyUsage
	}

	return parseExtKeyUsagesValue(parsedKeyUsages, role.ExtKeyUsage)
}

func parseExtKeyUsagesValue(parsedKeyUsages certutil.CertExtKeyUsage, extKeyUsages []string) certutil.CertExtKeyUsage {
	for _, k := range extKeyUsages {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "any":
			parsedKeyUsages |= certutil.AnyExtKeyUsage
		case "serverauth":
			parsedKeyUsages |= certutil.ServerAuthExtKeyUsage
		case "clientauth":
			parsedKeyUsages |= certutil.ClientAuthExtKeyUsage
		case "codesigning":
			parsedKeyUsages |= certutil.CodeSigningExtKeyUsage
		case "emailprotection":
			parsedKeyUsages |= certutil.EmailProtectionExtKeyUsage
		case "ipsecendsystem":
			parsedKeyUsages |= certutil.IpsecEndSystemExtKeyUsage
		case "ipsectunnel":
			parsedKeyUsages |= certutil.IpsecTunnelExtKeyUsage
		case "ipsecuser":
			parsedKeyUsages |= certutil.IpsecUserExtKeyUsage
		case "timestamping":
			parsedKeyUsages |= certutil.TimeStampingExtKeyUsage
		case "ocspsigning":
			parsedKeyUsages |= certutil.OcspSigningExtKeyUsage
		case "microsoftservergatedcrypto":
			parsedKeyUsages |= certutil.MicrosoftServerGatedCryptoExtKeyUsage
		case "netscapeservergatedcrypto":
			parsedKeyUsages |= certutil.NetscapeServerGatedCryptoExtKeyUsage
		}
	}

	return parsedKeyUsages
}

type roleEntry struct {
	LeaseMax                      string        `json:"lease_max"`
	Lease                         string        `json:"lease"`
	DeprecatedMaxTTL              string        `json:"max_ttl"`
	DeprecatedTTL                 string        `json:"ttl"`
	TTL                           time.Duration `json:"ttl_duration"`
	MaxTTL                        time.Duration `json:"max_ttl_duration"`
	AllowLocalhost                bool          `json:"allow_localhost"`
	AllowedBaseDomain             string        `json:"allowed_base_domain"`
	AllowedDomainsOld             string        `json:"allowed_domains,omitempty"`
	AllowedDomains                []string      `json:"allowed_domains_list"`
	AllowedDomainsTemplate        bool          `json:"allowed_domains_template"`
	AllowBaseDomain               bool          `json:"allow_base_domain"`
	AllowBareDomains              bool          `json:"allow_bare_domains"`
	AllowTokenDisplayName         bool          `json:"allow_token_displayname"`
	AllowSubdomains               bool          `json:"allow_subdomains"`
	AllowGlobDomains              bool          `json:"allow_glob_domains"`
	AllowWildcardCertificates     *bool         `json:"allow_wildcard_certificates,omitempty"`
	AllowAnyName                  bool          `json:"allow_any_name"`
	EnforceHostnames              bool          `json:"enforce_hostnames"`
	AllowIPSANs                   bool          `json:"allow_ip_sans"`
	ServerFlag                    bool          `json:"server_flag"`
	ClientFlag                    bool          `json:"client_flag"`
	CodeSigningFlag               bool          `json:"code_signing_flag"`
	EmailProtectionFlag           bool          `json:"email_protection_flag"`
	UseCSRCommonName              bool          `json:"use_csr_common_name"`
	UseCSRSANs                    bool          `json:"use_csr_sans"`
	KeyType                       string        `json:"key_type"`
	KeyBits                       int           `json:"key_bits"`
	UsePSS                        bool          `json:"use_pss"`
	SignatureBits                 int           `json:"signature_bits"`
	MaxPathLength                 *int          `json:",omitempty"`
	KeyUsageOld                   string        `json:"key_usage,omitempty"`
	KeyUsage                      []string      `json:"key_usage_list"`
	ExtKeyUsage                   []string      `json:"extended_key_usage_list"`
	OUOld                         string        `json:"ou,omitempty"`
	OU                            []string      `json:"ou_list"`
	OrganizationOld               string        `json:"organization,omitempty"`
	Organization                  []string      `json:"organization_list"`
	Country                       []string      `json:"country"`
	Locality                      []string      `json:"locality"`
	Province                      []string      `json:"province"`
	StreetAddress                 []string      `json:"street_address"`
	PostalCode                    []string      `json:"postal_code"`
	GenerateLease                 *bool         `json:"generate_lease,omitempty"`
	NoStore                       bool          `json:"no_store"`
	RequireCN                     bool          `json:"require_cn"`
	CNValidations                 []string      `json:"cn_validations"`
	AllowedOtherSANs              []string      `json:"allowed_other_sans"`
	AllowedSerialNumbers          []string      `json:"allowed_serial_numbers"`
	AllowedUserIDs                []string      `json:"allowed_user_ids"`
	AllowedURISANs                []string      `json:"allowed_uri_sans"`
	AllowedURISANsTemplate        bool          `json:"allowed_uri_sans_template"`
	PolicyIdentifiers             []string      `json:"policy_identifiers"`
	ExtKeyUsageOIDs               []string      `json:"ext_key_usage_oids"`
	BasicConstraintsValidForNonCA bool          `json:"basic_constraints_valid_for_non_ca"`
	NotBeforeDuration             time.Duration `json:"not_before_duration"`
	NotBefore                     string        `json:"not_before"`
	NotAfter                      string        `json:"not_after"`
	Issuer                        string        `json:"issuer"`
	// Name is only set when the role has been stored, on the fly roles have a blank name
	Name string `json:"-"`
}

func (r *roleEntry) ToResponseData() map[string]interface{} {
	responseData := map[string]interface{}{
		"ttl":                                int64(r.TTL.Seconds()),
		"max_ttl":                            int64(r.MaxTTL.Seconds()),
		"allow_localhost":                    r.AllowLocalhost,
		"allowed_domains":                    r.AllowedDomains,
		"allowed_domains_template":           r.AllowedDomainsTemplate,
		"allow_bare_domains":                 r.AllowBareDomains,
		"allow_token_displayname":            r.AllowTokenDisplayName,
		"allow_subdomains":                   r.AllowSubdomains,
		"allow_glob_domains":                 r.AllowGlobDomains,
		"allow_wildcard_certificates":        r.AllowWildcardCertificates,
		"allow_any_name":                     r.AllowAnyName,
		"allowed_uri_sans_template":          r.AllowedURISANsTemplate,
		"enforce_hostnames":                  r.EnforceHostnames,
		"allow_ip_sans":                      r.AllowIPSANs,
		"server_flag":                        r.ServerFlag,
		"client_flag":                        r.ClientFlag,
		"code_signing_flag":                  r.CodeSigningFlag,
		"email_protection_flag":              r.EmailProtectionFlag,
		"use_csr_common_name":                r.UseCSRCommonName,
		"use_csr_sans":                       r.UseCSRSANs,
		"key_type":                           r.KeyType,
		"key_bits":                           r.KeyBits,
		"signature_bits":                     r.SignatureBits,
		"use_pss":                            r.UsePSS,
		"key_usage":                          r.KeyUsage,
		"ext_key_usage":                      r.ExtKeyUsage,
		"ext_key_usage_oids":                 r.ExtKeyUsageOIDs,
		"ou":                                 r.OU,
		"organization":                       r.Organization,
		"country":                            r.Country,
		"locality":                           r.Locality,
		"province":                           r.Province,
		"street_address":                     r.StreetAddress,
		"postal_code":                        r.PostalCode,
		"no_store":                           r.NoStore,
		"allowed_other_sans":                 r.AllowedOtherSANs,
		"allowed_serial_numbers":             r.AllowedSerialNumbers,
		"allowed_user_ids":                   r.AllowedUserIDs,
		"allowed_uri_sans":                   r.AllowedURISANs,
		"require_cn":                         r.RequireCN,
		"cn_validations":                     r.CNValidations,
		"policy_identifiers":                 r.PolicyIdentifiers,
		"basic_constraints_valid_for_non_ca": r.BasicConstraintsValidForNonCA,
		"not_before_duration":                int64(r.NotBeforeDuration.Seconds()),
		"not_before":                         r.NotBefore,
		"not_after":                          r.NotAfter,
		"issuer_ref":                         r.Issuer,
	}
	if r.MaxPathLength != nil {
		responseData["max_path_length"] = r.MaxPathLength
	}
	if r.GenerateLease != nil {
		responseData["generate_lease"] = r.GenerateLease
	}
	return responseData
}

func checkCNValidations(validations []string) ([]string, error) {
	var haveDisabled bool
	var haveEmail bool
	var haveHostname bool

	var result []string

	if len(validations) == 0 {
		return []string{"email", "hostname"}, nil
	}

	for _, validation := range validations {
		switch strings.ToLower(validation) {
		case "disabled":
			if haveDisabled {
				return nil, errors.New("cn_validations value incorrect: `disabled` specified multiple times")
			}
			haveDisabled = true
		case "email":
			if haveEmail {
				return nil, errors.New("cn_validations value incorrect: `email` specified multiple times")
			}
			haveEmail = true
		case "hostname":
			if haveHostname {
				return nil, errors.New("cn_validations value incorrect: `hostname` specified multiple times")
			}
			haveHostname = true
		default:
			return nil, fmt.Errorf("cn_validations value incorrect: unknown type: `%s`", validation)
		}

		result = append(result, strings.ToLower(validation))
	}

	if !haveDisabled && !haveEmail && !haveHostname {
		return nil, errors.New("cn_validations value incorrect: must specify a value (`email` and/or `hostname`) or `disabled`")
	}

	if haveDisabled && (haveEmail || haveHostname) {
		return nil, errors.New("cn_validations value incorrect: cannot specify `disabled` along with `email` or `hostname`")
	}

	return result, nil
}

const pathListRolesHelpSyn = `List the existing roles in this backend`

const pathListRolesHelpDesc = `Roles will be listed by the role name.`

const pathRoleHelpSyn = `Manage the roles that can be created with this backend.`

const pathRoleHelpDesc = `This path lets you manage the roles that can be created with this backend.`

const policyIdentifiersParam = "policy_identifiers"

func getPolicyIdentifier(data *framework.FieldData, defaultIdentifiers *[]string) []string {
	policyIdentifierEntry, ok := data.GetOk(policyIdentifiersParam)
	if !ok {
		// No Entry for policy_identifiers
		if defaultIdentifiers != nil {
			return *defaultIdentifiers
		}
		return data.Get(policyIdentifiersParam).([]string)
	}
	// Could Be A JSON Entry
	policyIdentifierJsonEntry := data.Raw[policyIdentifiersParam]
	policyIdentifierJsonString, ok := policyIdentifierJsonEntry.(string)
	if ok {
		policyIdentifiers, err := parsePolicyIdentifiersFromJson(policyIdentifierJsonString)
		if err == nil {
			return policyIdentifiers
		}
	}
	// Else could Just Be A List of OIDs
	return policyIdentifierEntry.([]string)
}

func parsePolicyIdentifiersFromJson(policyIdentifiers string) ([]string, error) {
	var entries []certutil.PolicyIdentifierWithQualifierEntry
	var policyIdentifierList []string
	err := json.Unmarshal([]byte(policyIdentifiers), &entries)
	if err != nil {
		return policyIdentifierList, err
	}
	policyIdentifierList = make([]string, 0, len(entries))
	for _, entry := range entries {
		policyString, err := json.Marshal(entry)
		if err != nil {
			return policyIdentifierList, err
		}
		policyIdentifierList = append(policyIdentifierList, string(policyString))
	}
	return policyIdentifierList, nil
}
