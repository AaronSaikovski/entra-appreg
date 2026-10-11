package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/keyvault"
)

// vaultTarget is where create stores the client secret.
type vaultTarget struct {
	uri  string // normalized https://<vault>.vault.azure.net
	name string
}

// resolveVault validates --keyvault-uri and --keyvault-secret-name. A nil
// target means no vault was requested. Every returned error is a
// *cli.UsageError.
func resolveVault(o *cli.Options) (*vaultTarget, error) {
	if o.KeyVaultURI == nil && o.KeyVaultSecretName == nil {
		return nil, nil
	}
	if !o.CreateSecret {
		return nil, cli.Usagef("--keyvault-uri and --keyvault-secret-name require --create-secret.")
	}
	if o.KeyVaultURI == nil || o.KeyVaultSecretName == nil {
		return nil, cli.Usagef("--keyvault-uri and --keyvault-secret-name must be given together.")
	}
	uri, ok := keyvault.ParseVaultURI(*o.KeyVaultURI)
	if !ok {
		return nil, cli.Usagef("--keyvault-uri must look like https://<vault-name>.vault.azure.net, got: %s", *o.KeyVaultURI)
	}
	if !keyvault.ValidSecretName(*o.KeyVaultSecretName) {
		return nil, cli.Usagef("--keyvault-secret-name must be 1-127 letters, digits or hyphens, got: %s", *o.KeyVaultSecretName)
	}
	return &vaultTarget{uri: uri, name: *o.KeyVaultSecretName}, nil
}

// openVault gets the Key Vault store before anything is created.
func openVault(ctx context.Context, env Env, target *vaultTarget) (SecretStore, error) {
	if env.KeyVault == nil {
		return nil, errors.New("Key Vault is not available in this environment.")
	}
	return env.KeyVault(ctx, target.uri)
}

// storeSecret writes the new client secret to the vault, with the secret's
// expiry and start as Key Vault attributes and the client ID as a tag. On
// failure the secret stays in the report and the error is returned so
// create exits 1 after writing it.
func storeSecret(ctx context.Context, env Env, store SecretStore, plan createPlan, clientID string, secret *createdSecret) error {
	target := plan.keyVault
	fmt.Fprintf(env.Stdout, "Storing client secret in Key Vault as '%s' (%s)\n", target.name, target.uri)
	tags := map[string]string{"entra-client-id": clientID}
	if !secret.keyMissing {
		tags["entra-key-id"] = secret.keyID
	}
	id, err := store.SetSecret(ctx, keyvault.Secret{
		Name:      target.name,
		Value:     secret.value,
		Expires:   secret.endTime,
		NotBefore: plan.lifetime.start,
		Tags:      tags,
	})
	if err != nil {
		secret.vaultID = "(not stored)"
		fmt.Fprintln(env.Stderr, "Could not store the client secret in Key Vault; it is kept in the report.")
		return err
	}
	secret.vaultID = id
	fmt.Fprintf(env.Stdout, "Key Vault secret ID: %s\n", id)
	return nil
}
