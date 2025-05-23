package kv

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/helper/locksutil"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathDestroy returns the path configuration for the destroy endpoint
func pathDestroy(b *versionedKVBackend) *framework.Path {
	return &framework.Path{
		Pattern: "destroy/" + framework.MatchAllRegex("path"),
		Fields: map[string]*framework.FieldSchema{
			"path": {
				Type:        framework.TypeString,
				Description: "Location of the secret.",
			},
			"versions": {
				Type:        framework.TypeCommaIntSlice,
				Description: "The versions to destroy. Their data will be permanently deleted.",
			},
		},
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.upgradeCheck(b.pathDestroyWrite()),
			},
			logical.CreateOperation: &framework.PathOperation{
				Callback: b.upgradeCheck(b.pathDestroyWrite()),
			},
		},

		HelpSynopsis:    destroyHelpSyn,
		HelpDescription: destroyHelpDesc,
	}
}

func (b *versionedKVBackend) pathDestroyWrite() framework.OperationFunc {
	return func(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
		key := data.Get("path").(string)

		versions := data.Get("versions").([]int)
		if len(versions) == 0 {
			return logical.ErrorResponse("no version number provided"), logical.ErrInvalidRequest
		}

		lock := locksutil.LockForKey(b.locks, key)
		lock.Lock()
		defer lock.Unlock()

		// Create a transaction if we can.
		originalStorage := req.Storage
		if txnStorage, ok := req.Storage.(logical.TransactionalStorage); ok {
			txn, err := txnStorage.BeginTx(ctx)
			if err != nil {
				return nil, err
			}

			defer txn.Rollback(ctx)
			req.Storage = txn
		}

		meta, err := b.getKeyMetadata(ctx, req.Storage, key)
		if err != nil {
			return nil, err
		}
		if meta == nil {
			return nil, nil
		}

		for _, verNum := range versions {
			// If there is no version, or the version is already destroyed,
			// continue
			lv := meta.Versions[uint64(verNum)]
			if lv == nil || lv.Destroyed {
				continue
			}

			lv.Destroyed = true
		}

		// Write the metadata key before deleting the versions
		err = b.writeKeyMetadata(ctx, req.Storage, meta)
		if err != nil {
			return nil, err
		}

		for _, verNum := range versions {
			// Delete versioned data
			versionKey, err := b.getVersionKey(ctx, key, uint64(verNum), req.Storage)
			if err != nil {
				return nil, err
			}

			err = req.Storage.Delete(ctx, versionKey)
			if err != nil {
				return nil, err
			}
		}

		// Commit our transaction if we created one! We're done making
		// modifications to storage.
		if txn, ok := req.Storage.(logical.Transaction); ok && req.Storage != originalStorage {
			if err := txn.Commit(ctx); err != nil {
				return nil, err
			}
			req.Storage = originalStorage
		}

		return nil, nil
	}
}

const (
	destroyHelpSyn  = `Permanently removes one or more versions in the KV store`
	destroyHelpDesc = `
Permanently removes the specified version data for the provided key and version
numbers from the key-value store.
`
)
