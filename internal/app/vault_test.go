package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
	"github.com/AaronSaikovski/entra-appreg/internal/graphtest"
	"github.com/AaronSaikovski/entra-appreg/internal/keyvault"
)

var vaultArgs = []string{
	"--name", "App", "--redirect-urls", "http://localhost:5173/",
	"--create-secret", "--secret-expiry", "90",
	"--keyvault-uri", "https://My-Vault.vault.azure.net/", "--keyvault-secret-name", "app-secret",
}

// fakeStore records what create asks Key Vault to store.
type fakeStore struct {
	got []keyvault.Secret
	err error
}

func (s *fakeStore) SetSecret(_ context.Context, secret keyvault.Secret) (string, error) {
	s.got = append(s.got, secret)
	if s.err != nil {
		return "", s.err
	}
	return "https://my-vault.vault.azure.net/secrets/app-secret/v1", nil
}

// vaultEnv wires store into tio and records the vault URI and how many Graph
// requests had been made when the store was opened.
func vaultEnv(tio *testIO, f *graphtest.Fixture, store SecretStore, openErr error) (uri *string, requestsAtOpen *int) {
	uri, requestsAtOpen = new(string), new(int)
	*requestsAtOpen = -1
	tio.env.KeyVault = func(_ context.Context, vaultURI string) (SecretStore, error) {
		*uri, *requestsAtOpen = vaultURI, len(f.Requests())
		if openErr != nil {
			return nil, openErr
		}
		return store, nil
	}
	return uri, requestsAtOpen
}

func createResponses() []graphtest.Response {
	return []graphtest.Response{
		ok(obj{"value": arr{}}),
		org,
		graphtest.JSON(201, obj{"id": "object", "appId": "client"}),
		graphtest.JSON(201, obj{"id": "sp", "appId": "client"}),
		ok(obj{"keyId": keyID, "secretText": "fake-one-time-secret", "endDateTime": "2028-05-28T16:30:45Z"}),
	}
}

func TestCreateStoresSecretInKeyVaultAndReportsIt(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, createResponses()...)
	store := &fakeStore{}
	uri, requestsAtOpen := vaultEnv(&tio, f, store, nil)
	if err := create(context.Background(), options(t, vaultArgs...), f.Client(t), tio.env); err != nil {
		t.Fatal(err)
	}
	// The vault is opened (token acquired) after the read-only checks and
	// before the application POST.
	if *requestsAtOpen != 2 || *uri != "https://my-vault.vault.azure.net" {
		t.Errorf("vault opened after %d requests with %q", *requestsAtOpen, *uri)
	}
	if len(store.got) != 1 {
		t.Fatalf("%d secrets stored", len(store.got))
	}
	s := store.got[0]
	wantExp := time.Date(2028, 5, 28, 16, 30, 45, 0, time.UTC)
	if s.Name != "app-secret" || s.Value != "fake-one-time-secret" || !s.Expires.Equal(wantExp) || s.NotBefore != nil ||
		s.Tags["entra-client-id"] != "client" || s.Tags["entra-key-id"] != keyID {
		t.Errorf("stored %+v", s)
	}
	stdout := tio.stdout.String()
	for _, want := range []string{
		"Adding client secret 'App secret' to object\nStoring client secret in Key Vault as 'app-secret' (https://my-vault.vault.azure.net)\n",
		"Key Vault secret ID: https://my-vault.vault.azure.net/secrets/app-secret/v1\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "fake-one-time-secret") {
		t.Errorf("secret leaked to stdout:\n%s", stdout)
	}
	report := readReport(t, filepath.Join(tio.env.ReportDir, "App.txt"))
	want := "Secret expires:          2028-05-28T16:30:45Z\n" +
		"Key Vault secret:        app-secret in https://my-vault.vault.azure.net\n" +
		"Key Vault secret ID:     https://my-vault.vault.azure.net/secrets/app-secret/v1\n"
	if !strings.Contains(report, want) {
		t.Errorf("report lacks %q:\n%s", want, report)
	}
}

func TestCreateKeyVaultStoreFailureKeepsSecretInReportAndExits1(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, createResponses()...)
	vaultEnv(&tio, f, &fakeStore{err: errors.New("vault-denied")}, nil)
	err := create(context.Background(), options(t, vaultArgs...), f.Client(t), tio.env)
	if err == nil || !strings.Contains(err.Error(), "vault-denied") || isUsage(err) {
		t.Fatalf("err = %v", err)
	}
	report := readReport(t, filepath.Join(tio.env.ReportDir, "App.txt"))
	for _, want := range []string{"Secret value:            fake-one-time-secret\n", "Key Vault secret ID:     (not stored)\n"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if !strings.Contains(tio.stderr.String(), "Could not store the client secret in Key Vault; it is kept in the report.") {
		t.Errorf("stderr = %q", tio.stderr.String())
	}
}

func TestCreateKeyVaultTokenFailureStopsBeforePost(t *testing.T) {
	tio := newTestIO(t, "")
	f := graphtest.New(t, ok(obj{"value": arr{}}), org)
	vaultEnv(&tio, f, nil, errors.New("Key Vault authentication failed: no token"))
	err := create(context.Background(), options(t, vaultArgs...), f.Client(t), tio.env)
	if err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
	assertNoMethod(t, f.Requests(), "POST")
	if n := dirEntries(t, tio.env.ReportDir); n != 0 {
		t.Errorf("%d files written", n)
	}
}

func TestCreateKeyVaultOptionsValidateBeforeAnyRequest(t *testing.T) {
	base := []string{"--name", "App"}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--keyvault-uri", "https://v1.vault.azure.net", "--keyvault-secret-name", "s"},
			"--keyvault-uri and --keyvault-secret-name require --create-secret."},
		{[]string{"--create-secret", "--keyvault-uri", "https://v1.vault.azure.net"},
			"--keyvault-uri and --keyvault-secret-name must be given together."},
		{[]string{"--create-secret", "--keyvault-secret-name", "s"},
			"--keyvault-uri and --keyvault-secret-name must be given together."},
		{[]string{"--create-secret", "--keyvault-uri", "https://v1.vault.azure.net.evil.io", "--keyvault-secret-name", "s"},
			"--keyvault-uri must look like https://<vault-name>.vault.azure.net, got: https://v1.vault.azure.net.evil.io"},
		{[]string{"--create-secret", "--keyvault-uri", "https://vault1.vault.azure.net", "--keyvault-secret-name", "bad_name"},
			"--keyvault-secret-name must be 1-127 letters, digits or hyphens, got: bad_name"},
		// Secret lifetime errors come first.
		{[]string{"--create-secret", "--secret-expiry", "1", "--keyvault-uri", "bad"},
			"--secret-expiry must be one of 90, 180, 365, 545, 730 or custom, got: 1"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			tio := newTestIO(t, "")
			f := graphtest.New(t)
			_, requestsAtOpen := vaultEnv(&tio, f, &fakeStore{}, nil)
			err := create(context.Background(), options(t, append(base, tc.args...)...), f.Client(t), tio.env)
			usage, isUsageErr := errors.AsType[*cli.UsageError](err)
			if !isUsageErr || usage.Message != tc.want {
				t.Errorf("err = %v, want usage error %q", err, tc.want)
			}
			if n := len(f.Requests()); n != 0 || *requestsAtOpen != -1 {
				t.Errorf("%d requests, vault opened = %v", n, *requestsAtOpen != -1)
			}
		})
	}
}
