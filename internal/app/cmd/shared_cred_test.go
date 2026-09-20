package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
)

// fakeAccessSSM is an in-memory SSM for the shared-credential store.
type fakeAccessSSM struct {
	params map[string]string
	getErr error
	putErr error
	puts   int
}

func (f *fakeAccessSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.params[*in.Name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: awssdk.String(v)}}, nil
}

func (f *fakeAccessSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.puts++
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.params == nil {
		f.params = map[string]string{}
	}
	f.params[*in.Name] = *in.Value
	return &ssm.PutParameterOutput{}, nil
}

func (f *fakeAccessSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	delete(f.params, *in.Name)
	return &ssm.DeleteParameterOutput{}, nil
}

func newTestStore(f *fakeAccessSSM) *sharedCredStore {
	return &sharedCredStore{ssm: f, prefix: "km", kmsKey: "alias/km-platform-km-use1"}
}

// withTestStore points NewSharedCredStoreFunc at f for the test's lifetime.
func withTestStore(t *testing.T, f *fakeAccessSSM) {
	t.Helper()
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return newTestStore(f), nil }
	t.Cleanup(func() { NewSharedCredStoreFunc = saved })
}

// genKeyPEM makes a real ed25519 OpenSSH PEM so PublicKeyLine can derive from it.
func genKeyPEM(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	if _, err := sshkey.GenerateAndWrite(filepath.Join(dir, "k"), filepath.Join(dir, "k.pub"), "km-sbx"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// seedLocal writes a local credential file under $HOME the way km create would.
func seedLocal(t *testing.T, home, rel string, content []byte) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSyncSharedCredential_PullWhenLocalAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pem := genKeyPEM(t)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(pem)}}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, pem) {
		t.Error("local private key != SSM value")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %o, want 0600", st.Mode().Perm())
	}
	pub, err := os.ReadFile(p + ".pub")
	if err != nil {
		t.Fatalf(".pub not written: %v", err)
	}
	want, _ := sshkey.PublicKeyLine(pem, "km-sbx")
	if strings.TrimSpace(string(pub)) != want {
		t.Errorf(".pub = %q, want %q", pub, want)
	}
	if !strings.Contains(out.String(), "Pulled shared key from SSM") {
		t.Errorf("message: %q", out.String())
	}
	if f.puts != 0 {
		t.Error("start must never write SSM when SSM already has a value")
	}
}

func TestSyncSharedCredential_RefreshWhenLocalDiffers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := genKeyPEM(t)
	newer := genKeyPEM(t)
	local := seedLocal(t, home, filepath.Join(".km", "keys", "sbx"), old)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(newer)}}
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(local)
	if !bytes.Equal(got, newer) {
		t.Error("local key was not refreshed from SSM")
	}
	if !strings.Contains(out.String(), "refreshed from SSM (rekeyed elsewhere)") {
		t.Errorf("message: %q", out.String())
	}
	if f.puts != 0 {
		t.Error("start must never write SSM when SSM already has a value")
	}
	if _, err := os.Stat(local + ".new"); !os.IsNotExist(err) {
		t.Error("temp .new file must not be left behind")
	}
}

func TestSyncSharedCredential_SilentWhenSame(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pem := genKeyPEM(t)
	seedLocal(t, home, filepath.Join(".km", "keys", "sbx"), pem)
	// A trailing-newline difference must not read as "differs".
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": strings.TrimRight(string(pem), "\n")}}
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("expected silence, got %q", out.String())
	}
}

func TestSyncSharedCredential_PublishWhenSSMAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedLocal(t, home, filepath.Join(".km", "desktop", "sbx"), []byte("kasm:hunter2"))
	f := &fakeAccessSSM{}
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), desktopCredKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	if f.params["/km/access/sbx/desktop-cred"] != "kasm:hunter2" {
		t.Errorf("SSM after publish = %v", f.params)
	}
	if !strings.Contains(out.String(), "Published existing desktop credential to SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_PublishFailureIsWarnNotError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := seedLocal(t, home, filepath.Join(".km", "desktop", "sbx"), []byte("kasm:hunter2"))
	f := &fakeAccessSSM{putErr: errors.New("denied")}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), desktopCredKind, "sbx", &out)
	if err != nil || p != local {
		t.Fatalf("got (%q,%v), want (%q,nil)", p, err, local)
	}
	if !strings.Contains(out.String(), "[warn] could not publish desktop credential to SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_BothAbsentNamesRekey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := syncSharedCredential(context.Background(), newTestStore(&fakeAccessSSM{}), sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km vscode rekey sbx") {
		t.Errorf("err = %v, want it to name km vscode rekey sbx", err)
	}
	_, err = syncSharedCredential(context.Background(), newTestStore(&fakeAccessSSM{}), desktopCredKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km desktop rekey sbx") {
		t.Errorf("err = %v, want it to name km desktop rekey sbx", err)
	}
}

func TestSyncSharedCredential_ReadErrorFailsOpenWithLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := seedLocal(t, home, filepath.Join(".km", "keys", "sbx"), genKeyPEM(t))
	f := &fakeAccessSSM{getErr: errors.New("ThrottlingException")}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out)
	if err != nil || p != local {
		t.Fatalf("got (%q,%v), want local path and nil", p, err)
	}
	if !strings.Contains(out.String(), "[warn] could not read shared key from SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_ReadErrorWithNoLocalIsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := &fakeAccessSSM{getErr: errors.New("ThrottlingException")}
	_, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "ThrottlingException") {
		t.Errorf("err = %v", err)
	}
}

func TestSyncSharedCredential_NilStoreIsLegacyBehaviour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := syncSharedCredential(context.Background(), nil, sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "copy the ~/.km/keys/sbx* files over") {
		t.Errorf("nil store must keep today's error text; got %v", err)
	}
	_, err = syncSharedCredential(context.Background(), nil, desktopCredKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "copy the ~/.km/desktop/sbx file over") {
		t.Errorf("nil store must keep today's error text; got %v", err)
	}
}

func TestPublishSharedCredential_NilStoreIsNoop(t *testing.T) {
	// TestMain sets NewSharedCredStoreFunc to return (nil, nil).
	if err := publishSharedCredential(context.Background(), nil, sshKeyKind, "sbx", []byte("PEM")); err != nil {
		t.Errorf("nil store must be a no-op, got %v", err)
	}
}

func TestPublishSharedCredential_WritesParamVerbatim(t *testing.T) {
	f := &fakeAccessSSM{}
	withTestStore(t, f)
	if err := publishSharedCredential(context.Background(), nil, desktopCredKind, "sbx", []byte("kasm:pw")); err != nil {
		t.Fatal(err)
	}
	if f.params["/km/access/sbx/desktop-cred"] != "kasm:pw" {
		t.Errorf("params = %v", f.params)
	}
}

func TestSandboxKeyPath_PullsFromSSM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pem := genKeyPEM(t)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(pem)}}
	withTestStore(t, f)

	p, err := sandboxKeyPath(context.Background(), nil, "sbx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("key not materialised at %s: %v", p, err)
	}
	if _, err := os.Stat(p + ".pub"); err != nil {
		t.Fatalf(".pub not materialised: %v", err)
	}
}

func TestSandboxKeyPath_StoreErrorFallsBackToLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := seedLocal(t, home, filepath.Join(".km", "keys", "sbx"), genKeyPEM(t))
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return nil, errors.New("sso expired") }
	defer func() { NewSharedCredStoreFunc = saved }()

	p, err := sandboxKeyPath(context.Background(), nil, "sbx")
	if err != nil || p != local {
		t.Errorf("got (%q,%v), want local path and nil", p, err)
	}
}

func TestAuthorizedKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	a, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "a"), filepath.Join(dir, "a.pub"), "km-x")
	b, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "b"), filepath.Join(dir, "b.pub"), "km-x")
	pubA := filepath.Join(dir, "a.pub")

	if authorizedKeyMismatch(a, pubA) {
		t.Error("same key must not mismatch")
	}
	if authorizedKeyMismatch(a+" trailing-comment-change", pubA) {
		t.Error("comment differences must not mismatch (fingerprint compare)")
	}
	if !authorizedKeyMismatch(b, pubA) {
		t.Error("different key must mismatch")
	}
	if authorizedKeyMismatch("", pubA) {
		t.Error("empty box line is not a mismatch")
	}
	if authorizedKeyMismatch("garbage not a key", pubA) {
		t.Error("unparseable box line is not a mismatch (pre-existing boxes must keep starting)")
	}
	if authorizedKeyMismatch(b, filepath.Join(dir, "missing.pub")) {
		t.Error("unreadable local pub is not a mismatch")
	}
	if authorizedKeyMismatch(b, "") {
		t.Error("empty local pub path disables the check")
	}
}

func TestParseVSCodeStatusWithKey_MismatchNamesRekey(t *testing.T) {
	dir := t.TempDir()
	_, _ = sshkey.GenerateAndWrite(filepath.Join(dir, "a"), filepath.Join(dir, "a.pub"), "km-sbx")
	other, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "b"), filepath.Join(dir, "b.pub"), "km-sbx")
	out := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\n" + other + "\n"
	err := parseVSCodeStatusWithKey(out, "sbx", filepath.Join(dir, "a.pub"))
	if err == nil || !strings.Contains(err.Error(), "km vscode rekey sbx") {
		t.Errorf("err = %v", err)
	}
	if err := parseVSCodeStatusWithKey(out, "sbx", ""); err != nil {
		t.Errorf("no local pub path must skip the check; got %v", err)
	}
	if err := parseVSCodeStatusWithKey(out, "sbx", filepath.Join(dir, "b.pub")); err != nil {
		t.Errorf("matching key must pass; got %v", err)
	}
}

func TestParseHerdrStatus_CapturesAuthKeysLine(t *testing.T) {
	out := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\nssh-ed25519 AAAA km-sbx\n=== herdr path ===\n\n=== herdr version ===\n"
	st := parseHerdrStatus(out)
	if st.AuthKeysLine != "ssh-ed25519 AAAA km-sbx" {
		t.Errorf("AuthKeysLine = %q", st.AuthKeysLine)
	}
}

func TestVSCodeRekey_PublishesToSSMAfterLocalCommit(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	seedRekeyTestKeys(t, homeDir, "sb-abc123")
	f := &fakeAccessSSM{}
	withTestStore(t, f)

	var gotErr error
	out := captureStdout(func() {
		gotErr = runVSCodeRekey(context.Background(), &config.Config{}, newVSCodeEC2Sandbox("sb-abc123"), newRunningEC2Mock(),
			&rekeyInstallSpyMock{preflightOutput: healthySSMOutput}, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	local, _ := os.ReadFile(filepath.Join(homeDir, ".km", "keys", "sb-abc123"))
	if got := f.params["/km/access/sb-abc123/ssh-key"]; got != string(local) {
		t.Errorf("SSM ssh-key != committed local key (ssm %d bytes, local %d bytes)", len(got), len(local))
	}
	if !strings.Contains(out, "Published to SSM") {
		t.Errorf("output: %s", out)
	}
}

func TestVSCodeRekey_PublishFailureNamesRerunAndKeepsLocal(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	orig, _ := seedRekeyTestKeys(t, homeDir, "sb-abc123")
	withTestStore(t, &fakeAccessSSM{putErr: errors.New("denied")})

	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(context.Background(), &config.Config{}, newVSCodeEC2Sandbox("sb-abc123"), newRunningEC2Mock(),
			&rekeyInstallSpyMock{preflightOutput: healthySSMOutput}, "sb-abc123", false, true)
	})
	if gotErr == nil || !strings.Contains(gotErr.Error(), "km vscode rekey sb-abc123 --yes") {
		t.Fatalf("err = %v, want it to name the re-run", gotErr)
	}
	priv := filepath.Join(homeDir, ".km", "keys", "sb-abc123")
	now, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(now, orig) {
		t.Error("local key must already be the NEW key (box → local → SSM order)")
	}
	if _, err := os.Stat(priv + ".new"); !os.IsNotExist(err) {
		t.Error("no .new scratch file may remain")
	}
}

func TestDesktopRekey_PublishesToSSM(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	seedLocal(t, homeDir, filepath.Join(".km", "desktop", "sb-abc123"), []byte("kasm:oldpass"))
	f := &fakeAccessSSM{}
	withTestStore(t, f)

	var gotErr error
	captureStdout(func() {
		gotErr = runDesktopRekey(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-abc123"), newRunningEC2Mock(),
			&vsCodeSSMMock{output: rekeyOKSSMOutput}, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	local, _ := os.ReadFile(filepath.Join(homeDir, ".km", "desktop", "sb-abc123"))
	got := f.params["/km/access/sb-abc123/desktop-cred"]
	if got == "" || got != string(local) || got == "kasm:oldpass" {
		t.Errorf("SSM desktop-cred = %q, local = %q", got, local)
	}
}

func TestPublishCreateCredential_PublishesFileContentNonFatally(t *testing.T) {
	home := t.TempDir()
	pem := genKeyPEM(t)
	local := seedLocal(t, home, filepath.Join(".km", "keys", "sbx"), pem)
	f := &fakeAccessSSM{}
	withTestStore(t, f)
	publishCreateCredential(context.Background(), nil, sshKeyKind, "sbx", local, nil)
	if f.params["/km/access/sbx/ssh-key"] != string(pem) {
		t.Error("create did not publish the generated key verbatim")
	}

	// A failing store must not panic or error — create keeps going.
	withTestStore(t, &fakeAccessSSM{putErr: errors.New("denied")})
	publishCreateCredential(context.Background(), nil, desktopCredKind, "sbx", "", []byte("kasm:pw"))
}

func TestPublishSharedCredential_StoreErrorPropagates(t *testing.T) {
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return nil, errors.New("sso expired") }
	defer func() { NewSharedCredStoreFunc = saved }()
	err := publishSharedCredential(context.Background(), nil, sshKeyKind, "sbx", []byte("PEM"))
	if err == nil || !strings.Contains(err.Error(), "sso expired") {
		t.Errorf("err = %v", err)
	}
}
