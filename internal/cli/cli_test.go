package cli

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func parseWords(words ...string) (Invocation, error) {
	inv, err := Parse(words)
	if err != nil {
		if _, ok := errors.AsType[*UsageError](err); !ok {
			panic("Parse returned a non-UsageError: " + err.Error())
		}
	}
	return inv, err
}

func mustRun(t *testing.T, words ...string) *Options {
	t.Helper()
	inv, err := parseWords(words...)
	if err != nil {
		t.Fatalf("%q: unexpected error: %v", words, err)
	}
	if inv.Kind != KindRun || inv.Options == nil {
		t.Fatalf("%q: expected a command, got %+v", words, inv)
	}
	return inv.Options
}

func expectErr(t *testing.T, words ...string) error {
	t.Helper()
	_, err := parseWords(words...)
	if err == nil {
		t.Fatalf("%q: expected an error", words)
	}
	return err
}

func expectHelp(t *testing.T, topic HelpTopic, words ...string) {
	t.Helper()
	inv, err := parseWords(words...)
	if err != nil || inv.Kind != KindHelp || inv.Topic != topic {
		t.Fatalf("%q: expected help topic %d, got %+v, %v", words, topic, inv, err)
	}
}

func expectVersion(t *testing.T, words ...string) {
	t.Helper()
	inv, err := parseWords(words...)
	if err != nil || inv.Kind != KindVersion {
		t.Fatalf("%q: expected version, got %+v, %v", words, inv, err)
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestDeleteRequiresIDAndRejectsUnrelatedOptions(t *testing.T) {
	if got := mustRun(t, "DeLeTe", "--appid", "id").Command; got != CommandDelete {
		t.Fatalf("command = %v", got)
	}
	for _, args := range [][]string{
		{"delete"},
		{"delete", "--appid"},
		{"delete", "--appid", ""},
		{"delete", "--appid", " "},
		{"delete", "--appid", "no-id"},
		{"delete", "--appid", "id", "--yes"},
		{"delete", "--appid", "id", "--force"},
	} {
		expectErr(t, args...)
	}
	for _, option := range []string{
		"--name", "--redirect-url", "--redirect-urls", "--audience", "--scope-name",
		"--scope-display-name", "--scope-description", "--scope-consent",
		"--scope-user-display-name", "--scope-user-description", "--scope-state",
		"--top", "--secret-expiry", "--secret-start", "--secret-end",
	} {
		expectErr(t, "delete", "--appid", "id", option, "")
	}
	for _, option := range []string{"--json", "--create-secret"} {
		expectErr(t, "delete", "--appid", "id", option)
	}
	expectHelp(t, TopicDelete, "delete", "--help")
	expectHelp(t, TopicDelete, "help", "delete")
	expectVersion(t, "delete", "--help", "--version")
}

func TestDeleteErrorMessages(t *testing.T) {
	if err := expectErr(t, "delete"); err.Error() != "delete requires --appid <application object id or client id>." {
		t.Fatalf("got %q", err)
	}
	err := expectErr(t, "delete", "--appid", "id", "--name", "x", "--json", "--scope-state", "", "--secret-end", "")
	want := "--name, --scope-state, --secret-end, --json can't be used with the delete command."
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err, want)
	}
}

func TestRepeatedScalarsOverrideButRedirectsAppendAndFlagsStaySet(t *testing.T) {
	o := mustRun(t, "create",
		"--appid", "old", "--appid", "new",
		"--name", "first", "--name", "",
		"--create-secret", "--create-secret",
		"--redirect-url", "https://a/First",
		"--redirect-urls", " , ",
		"--redirect-url", "https://a/Last",
	)
	if deref(o.AppID) != "new" || deref(o.Name) != "" || !o.CreateSecret || !o.RedirectsSupplied {
		t.Fatalf("unexpected options %+v", o)
	}
	if want := []string{"https://a/First", "https://a/Last"}; !reflect.DeepEqual(o.RedirectURIs, want) {
		t.Fatalf("redirects = %q", o.RedirectURIs)
	}
	o = mustRun(t, "list", "--top", "0", "--top", "2", "--json", "--json")
	if o.ListLimit != 2 || !o.JSON {
		t.Fatalf("unexpected options %+v", o)
	}
}

func TestCobraDoesNotBroadenSyntax(t *testing.T) {
	for _, args := range [][]string{
		{"--"},
		{"list", "--"},
		{"--name=value"},
		{"--name=x"},
		{"--redirect-url=a"},
		{"list", "--na", "prefix"},
		{"list", "-j"},
		{"-x"},
		{"-hh"},
		{"list", "-"},
		{"list", "help"},
		{"create", "extra"},
		{"create", "help"},
		{"list", "---name", "x"},
		{"list", "--json=true"},
		{"--create-secret=false"},
		{"list", "--name", "--unknown", "--name", "valid"},
		{"list", "--name", "valid", "extra"},
		{"list", "--completion"},
	} {
		expectErr(t, args...)
	}
	if err := expectErr(t, "--name=x"); err.Error() != "Unknown or incomplete argument: --name=x. Run with --help for usage." {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "create", "extra"); err.Error() != "Unknown or incomplete argument: extra. Run with --help for usage." {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "--name", "--oops"); !strings.Contains(err.Error(), "Expected a value, not a following long option.") {
		t.Fatalf("got %q", err)
	}
	inv, err := parseWords("--name=x", "--help")
	if err != nil || inv.Kind != KindHelp {
		t.Fatalf("got %+v, %v", inv, err)
	}
	expectVersion(t, "--", "--version")
}

func TestAliasAccumulatesWithCanonical(t *testing.T) {
	o := mustRun(t, "--redirect-url", "a", "--redirect-urls", "b,c")
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(o.RedirectURIs, want) || !o.RedirectsSupplied {
		t.Fatalf("redirects = %q, supplied = %v", o.RedirectURIs, o.RedirectsSupplied)
	}
}

func TestSingleHyphensAndEqualsRemainLiteralOptionValues(t *testing.T) {
	for _, value := range []string{"-", "-preview", "-x=y", "name=value", ""} {
		if got := deref(mustRun(t, "--name", value).Name); got != value {
			t.Fatalf("%q: name = %v", value, got)
		}
	}
	expectErr(t, "--name", "--not-a-value")
}

func TestEarlyExitPrecedenceIgnoresInvalidOptionsAndValues(t *testing.T) {
	expectVersion(t, "--name", "--help", "--version")
	expectHelp(t, TopicGeneral, "--name", "-h")
	expectHelp(t, TopicList, "LiSt", "--unknown", "--help")
	expectErr(t, "unknown", "--version", "--help")
	expectErr(t, "help", "unknown", "--version")
	expectHelp(t, TopicCreate, "help", "CREATE", "--version")
	inv, err := parseWords()
	if err != nil || inv.Kind != KindNoArguments {
		t.Fatalf("got %+v, %v", inv, err)
	}
	if err := expectErr(t, "unknown"); err.Error() != "Unknown command: unknown. Use 'create', 'expose-api', 'list', 'delete' or 'help' (see --help)." {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "help", "Nope"); err.Error() != "No help for 'nope'. Try: help, help create, help expose-api, help list, help delete." {
		t.Fatalf("got %q", err)
	}
}

func TestValuesPreserveRawSpellingAndRedirectsAccumulate(t *testing.T) {
	o := mustRun(t,
		"--name", "first", "--name", " second ",
		"--appid", "no-id",
		"--redirect-url", " https://a/A, ,https://a/a ",
		"--redirect-urls", "https://a/A",
		"--scope-description", "-single",
		"--secret-end", "",
	)
	if deref(o.Name) != " second " || deref(o.AppID) != "no-id" ||
		deref(o.ScopeDescription) != "-single" || deref(o.SecretEnd) != "" || !o.RedirectsSupplied {
		t.Fatalf("unexpected options %+v", o)
	}
	if want := []string{"https://a/A", "https://a/a", "https://a/A"}; !reflect.DeepEqual(o.RedirectURIs, want) {
		t.Fatalf("redirects = %q", o.RedirectURIs)
	}
	for _, words := range [][]string{
		{"--name"},
		{"--name", "--audience", "x"},
		{"--Name", "x"},
		{"--name=x"},
	} {
		expectErr(t, words...)
	}
}

func TestCreationSemanticsRemainDeferredButBlankIDsDoNot(t *testing.T) {
	if got := mustRun(t, "create").Command; got != CommandCreate {
		t.Fatalf("command = %v", got)
	}
	if got := deref(mustRun(t, "--appid", "existing").AppID); got != "existing" {
		t.Fatalf("appid = %v", got)
	}
	mustRun(t, "--name", "", "--audience", "invalid", "--scope-state", "invalid", "--secret-expiry", "invalid")
	if err := expectErr(t, "--appid", " \t"); err.Error() != "--appid cannot be blank; omit it when creating a new application." {
		t.Fatalf("got %q", err)
	}
	expectErr(t, "expose-api", "--appid", "no-id", "--scope-name", "read")
	if got := mustRun(t, "expose-api", "--appid", "NO-ID", "--scope-name", "read").Command; got != CommandExposeAPI {
		t.Fatalf("command = %v", got)
	}
	if err := expectErr(t, "expose-api", "--appid", "id", "--scope-name", " "); err.Error() != "expose-api requires --scope-name." {
		t.Fatalf("got %q", err)
	}
}

func TestListTopObeysSignedIntegerBoundaries(t *testing.T) {
	for raw, want := range map[string]int{
		" all ":      math.MaxInt32,
		"ALL":        math.MaxInt32,
		"2147483647": math.MaxInt32,
		" +1 ":       1,
		"999":        999,
	} {
		if got := mustRun(t, "list", "--top", raw).ListLimit; got != want {
			t.Fatalf("%q: limit = %d, want %d", raw, got, want)
		}
	}
	if got := mustRun(t, "list").ListLimit; got != 50 {
		t.Fatalf("default limit = %d", got)
	}
	for _, raw := range []string{"", "0", "-1", "2147483648", "1.0", "1_000", "allx"} {
		expectErr(t, "list", "--top", raw)
	}
	if err := expectErr(t, "list", "--top", "0"); err.Error() != "--top must be a positive whole number or 'all', got: 0" {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "list", "--name", " "); err.Error() != "--name cannot be blank." {
		t.Fatalf("got %q", err)
	}
}

func TestForbiddenOptionGroupsRejectEvenEmptyValues(t *testing.T) {
	for _, option := range []string{
		"--appid", "--redirect-url", "--audience", "--scope-name", "--scope-display-name",
		"--scope-description", "--scope-consent", "--scope-user-display-name",
		"--scope-user-description", "--scope-state", "--secret-expiry", "--secret-start",
		"--secret-end",
	} {
		expectErr(t, "list", option, "")
	}
	expectErr(t, "list", "--create-secret")
	for _, option := range []string{
		"--name", "--redirect-url", "--audience", "--top", "--secret-expiry", "--secret-start", "--secret-end",
	} {
		expectErr(t, "expose-api", "--appid", "id", "--scope-name", "read", option, "")
	}
	for _, option := range []string{"--json", "--create-secret"} {
		expectErr(t, "expose-api", "--appid", "id", "--scope-name", "read", option)
	}
	if err := expectErr(t, "create", "--json"); err.Error() != "--json can only be used with the list command." {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "create", "--top", "", "--json"); err.Error() != "--top, --json can only be used with the list command." {
		t.Fatalf("got %q", err)
	}
	if err := expectErr(t, "list", "--appid", "x", "--redirect-url", "a"); err.Error() != "--appid, --redirect-urls can't be used with the list command." {
		t.Fatalf("got %q", err)
	}
}

func TestHelpTextUsesBinaryNameAndGoCredential(t *testing.T) {
	for _, topic := range []HelpTopic{TopicGeneral, TopicCreate, TopicExposeAPI, TopicList, TopicDelete} {
		text := HelpText(topic)
		if strings.Contains(text, "cargo") || strings.Contains(text, "AzureCliCredential") || strings.Contains(text, "NOTE") {
			t.Fatalf("topic %d still has Rust-specific text", topic)
		}
		if !strings.Contains(text, "  entra-appreg ") {
			t.Fatalf("topic %d lacks entra-appreg usage", topic)
		}
		if topic != TopicDelete && !strings.Contains(text, "AzureCLICredential") {
			t.Fatalf("topic %d lacks AzureCLICredential", topic)
		}
	}
	general := HelpText(TopicGeneral)
	if !strings.HasPrefix(general, "entra-appreg "+Version+" - manage ") ||
		!strings.Contains(general, "Azure SDK for Go (azidentity) AzureCLICredential") {
		t.Fatalf("general help heading/authentication text wrong")
	}
}

func TestEqualFoldASCII(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"AzureADMyOrg", "azureadmyorg", true},
		{"all", "ALL", true},
		{"all", "alll", false},
		{"é", "É", false},
		{"K", "\u212a", false},
	} {
		if got := EqualFoldASCII(c.a, c.b); got != c.want {
			t.Fatalf("EqualFoldASCII(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
