package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

// cliEnv makes the test binary run as tsipam.
const cliEnv = "TSIPAM_TEST_RUN_AS_CLI"

const mcpExitTimeout = 10 * time.Second

// pinnedTools is the tool set. Adding a leaf command means deciding here whether
// an MCP client gets it.
func pinnedTools() []string {
	return []string{
		"tsipam-calc-aggregate", "tsipam-calc-info", "tsipam-calc-next-free", "tsipam-calc-split",
		"tsipam-check-policy", "tsipam-devices-list", "tsipam-plan",
		"tsipam-port-list", "tsipam-port-release", "tsipam-port-reserve",
		"tsipam-registry-history",
		"tsipam-service-list", "tsipam-service-release", "tsipam-service-reserve",
		"tsipam-site-id-list", "tsipam-site-id-release", "tsipam-site-id-reserve",
		"tsipam-subnet-allocate", "tsipam-subnet-list", "tsipam-subnet-release", "tsipam-subnet-reserve",
	}
}

// mcpSession is a running mcp-server and the client end of its stdio.
type mcpSession struct {
	t       *testing.T
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *bytes.Buffer
	tempDir string
	nextID  int
	// handshake is the initialize result.
	handshake mcpHandshake
}

type mcpHandshake struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type mcpTool struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"inputSchema"`
	Annotations struct {
		ReadOnlyHint    bool  `json:"readOnlyHint"`
		DestructiveHint *bool `json:"destructiveHint"`
	} `json:"annotations"`
}

// cliCommand is this test binary running as tsipam with exactly env.
func cliCommand(t *testing.T, env map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(t.Context(), os.Args[0], args...)
	command.Dir = t.TempDir()
	command.Env = []string{cliEnv + "=1", "PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		command.Env = append(command.Env, key+"="+env[key])
	}
	return command
}

func startMCP(t *testing.T, env map[string]string) *mcpSession {
	t.Helper()
	session := &mcpSession{t: t, stderr: &bytes.Buffer{}, tempDir: t.TempDir()}
	full := map[string]string{"TMPDIR": session.tempDir}
	maps.Copy(full, env)
	session.command = cliCommand(t, full, commandMCPServer)
	session.command.Stderr = session.stderr
	stdin, err := session.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := session.command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	session.stdin, session.stdout = stdin, bufio.NewReader(stdout)
	if err = session.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.command.Process.Kill() })
	result := session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"},
	})
	if err = json.Unmarshal(result, &session.handshake); err != nil {
		t.Fatal(err)
	}
	session.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return session
}

func (s *mcpSession) send(message map[string]any) {
	s.t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err = s.stdin.Write(append(raw, '\n')); err != nil {
		s.t.Fatalf("writing to the server: %v; stderr: %s", err, s.stderr)
	}
}

// request sends one request and returns its result. Anything on stdout that is
// not a protocol message fails the test.
func (s *mcpSession) request(method string, params map[string]any) json.RawMessage {
	s.t.Helper()
	s.nextID++
	s.send(map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	for {
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			s.t.Fatalf("reading from the server: %v; stderr: %s", err, s.stderr)
		}
		var message mcpMessage
		if err = json.Unmarshal(line, &message); err != nil || message.JSONRPC != "2.0" {
			s.t.Fatalf("stdout holds something other than a protocol message: %s", line)
		}
		if message.ID == nil || *message.ID != s.nextID {
			continue
		}
		if message.Error != nil {
			s.t.Fatalf("%s failed: %s", method, message.Error)
		}
		return message.Result
	}
}

func (s *mcpSession) tools() []mcpTool {
	s.t.Helper()
	var listed struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(s.request("tools/list", map[string]any{}), &listed); err != nil {
		s.t.Fatal(err)
	}
	return listed.Tools
}

// call calls a tool and returns its text and whether it failed.
func (s *mcpSession) call(tool string, arguments map[string]any) (string, bool) {
	s.t.Helper()
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	raw := s.request("tools/call", map[string]any{"name": tool, "arguments": arguments})
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Content) != 1 {
		s.t.Fatalf("%s returned %s: %v", tool, raw, err)
	}
	return result.Content[0].Text, result.IsError
}

func (s *mcpSession) mustCall(tool string, arguments map[string]any) string {
	s.t.Helper()
	text, failed := s.call(tool, arguments)
	if failed {
		s.t.Fatalf("%s failed: %s", tool, text)
	}
	return text
}

func (s *mcpSession) mustFail(tool string, arguments map[string]any) errorObject {
	s.t.Helper()
	text, failed := s.call(tool, arguments)
	if !failed {
		s.t.Fatalf("%s succeeded: %s", tool, text)
	}
	return decodeFailure(s.t, text)
}

// close ends the session as a client does and returns the exit code.
func (s *mcpSession) close() int {
	s.t.Helper()
	if err := s.stdin.Close(); err != nil {
		s.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.command.Wait() }()
	select {
	case <-done:
	case <-time.After(mcpExitTimeout):
		s.t.Fatalf("the server outlived its client; stderr: %s", s.stderr)
	}
	return s.command.ProcessState.ExitCode()
}

func toolNames(tools []mcpTool) []string {
	names := make([]string, len(tools))
	for index, tool := range tools {
		names[index] = tool.Name
	}
	slices.Sort(names)
	return names
}

func TestMCPServesThePinnedTools(t *testing.T) {
	t.Parallel()
	session := startMCP(t, nil)
	live := session.tools()
	if names := toolNames(live); !slices.Equal(names, pinnedTools()) {
		t.Errorf("tools = %v, want %v", names, pinnedTools())
	}

	_, stdout, _ := runWith(t, nil, "__schema", "--as=mcp")
	var static struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(stdout), &static); err != nil {
		t.Fatal(err)
	}
	if names := toolNames(static.Tools); !slices.Equal(names, pinnedTools()) {
		t.Errorf("__schema --as=mcp lists %v, want %v", names, pinnedTools())
	}
	assertToolHints(t, live)

	if want := resolvedVersion(); session.handshake.ServerInfo.Version != want ||
		session.handshake.ServerInfo.Name != toolName {
		t.Errorf("handshake = %+v, want %s %s", session.handshake, toolName, want)
	}
	if code := session.close(); code != exitOK {
		t.Errorf("exit = %d after stdin closed; stderr: %s", code, session.stderr)
	}
	if left, err := os.ReadDir(session.tempDir); err != nil || len(left) != 0 {
		t.Errorf("the server left %v, %v", left, err)
	}
}

func assertToolHints(t *testing.T, tools []mcpTool) {
	t.Helper()
	for _, tool := range tools {
		destructive := tool.Annotations.DestructiveHint
		switch {
		case strings.HasSuffix(tool.Name, "-reserve"), strings.HasSuffix(tool.Name, "-release"):
			if destructive == nil || !*destructive || tool.Annotations.ReadOnlyHint {
				t.Errorf("%s hints = %+v, want destructive", tool.Name, tool.Annotations)
			}
		case strings.HasSuffix(tool.Name, "-allocate"):
			if destructive == nil || *destructive || tool.Annotations.ReadOnlyHint {
				t.Errorf("%s hints = %+v, want additive", tool.Name, tool.Annotations)
			}
		default:
			if !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s hints = %+v, want read-only", tool.Name, tool.Annotations)
			}
		}
	}
}

// TestEveryLeafButCheckIsPinned ties the pinned list to the command tree.
func TestEveryLeafButCheckIsPinned(t *testing.T) {
	t.Parallel()
	var want []string
	for _, name := range leafNames() {
		if name != commandCheck {
			want = append(want, toolName+"-"+strings.ReplaceAll(name, " ", "-"))
		}
	}
	slices.Sort(want)
	if pinned := pinnedTools(); !slices.Equal(want, pinned) {
		t.Errorf("leaf commands give %v, pinned %v", want, pinned)
	}
}

func TestMCPServerOffersNoTransport(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{commandMCPServer, "--transport", "http"}, {commandMCPServer, "--addr", "127.0.0.1:0"},
		{commandMCPServer, "extra"},
	} {
		code, stdout, stderr := runWith(t, nil, args...)
		failure := decodeFailure(t, stderr)
		if code != exitUsage || stdout != "" || failure.ErrorCode != codeValidationError ||
			failure.Version != resolvedVersion() {
			t.Errorf("%v: exit = %d, failure = %+v", args, code, failure)
		}
	}
}

// TestMCPServerRefusesCallFlags covers flags a user might pass to make a whole
// session safe. Each call resolves its own, so the server would ignore them.
func TestMCPServerRefusesCallFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{
		"--dry-run", "--yes", "--idempotency-key=job-42", "--sanitize=identity", "--sanitize=off",
	} {
		code, stdout, stderr := runWith(t, nil, commandMCPServer, flag)
		name, _, _ := strings.Cut(flag, "=")
		failure := decodeFailure(t, stderr)
		if code != exitUsage || stdout != "" || failure.ErrorCode != codeValidationError ||
			!strings.Contains(failure.Message, name+" does not reach tool calls") {
			t.Errorf("%s: exit = %d, failure = %+v", flag, code, failure)
		}
	}
}

func TestMCPResultMatchesTheCommandLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet, writesRegistry)
	session := startMCP(t, nil)
	got := session.mustCall("tsipam-site-id-list", map[string]any{"registry-dir": dir})
	_, want, _ := runWith(t, nil, commandSiteID, commandList, "--registry-dir", dir, "--format", formatJSON)
	if maskRun(t, got) != maskRun(t, want) {
		t.Errorf("tool returned %s, command line %s", maskRun(t, got), maskRun(t, want))
	}
	// --strict must not swallow a result here either.
	strict := session.mustCall("tsipam-site-id-list", map[string]any{"registry-dir": dir, "strict": true})
	if maskRun(t, strict) != maskRun(t, want) {
		t.Errorf("strict tool returned %s, want %s", maskRun(t, strict), maskRun(t, want))
	}

	var split outputEnvelope
	if err := decodeData([]byte(session.mustCall("tsipam-calc-split", map[string]any{
		"cidr": "10.0.0.0/24", "prefix": 26,
	})), &split); err != nil {
		t.Fatal(err)
	}
	if split.Subnets == nil || len(*split.Subnets) != 4 {
		t.Errorf("split = %+v, want four subnets", split)
	}
	var aggregate outputEnvelope
	if err := decodeData([]byte(session.mustCall("tsipam-calc-aggregate", map[string]any{
		"cidr": []string{"10.0.0.0/25", "10.0.0.128/25"},
	})), &aggregate); err != nil {
		t.Fatal(err)
	}
	if aggregate.Supernets == nil || !slices.Equal(*aggregate.Supernets, []string{"10.0.0.0/24"}) {
		t.Errorf("aggregate = %+v", aggregate)
	}
	// The repeated flag does not carry over.
	if failure := session.mustFail("tsipam-calc-aggregate", nil); failure.ErrorCode != codeValidationError {
		t.Errorf("failure = %+v, want %s", failure, codeValidationError)
	}
}

// TestMCPRegistrySelectionIsPerCall covers the two selection flags, which refuse
// each other, across calls on one command tree.
func TestMCPRegistrySelectionIsPerCall(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet, writesRegistry)
	session := startMCP(t, nil)
	session.mustCall("tsipam-site-id-reserve", map[string]any{"id": "7", "site": "lab", "registry": url})
	fromFile := session.mustCall("tsipam-site-id-list", map[string]any{"registry-dir": dir})
	fromSQLite := session.mustCall("tsipam-site-id-list", map[string]any{"registry": url})
	if !strings.Contains(fromFile, `"site":"hq"`) || strings.Contains(fromFile, "lab") ||
		!strings.Contains(fromSQLite, `"site":"lab"`) || strings.Contains(fromSQLite, "hq") {
		t.Errorf("file registry = %s, sqlite registry = %s", fromFile, fromSQLite)
	}
	if failure := session.mustFail("tsipam-site-id-list", map[string]any{
		"registry": url, "registry-dir": dir,
	}); failure.ErrorCode != codeValidationError {
		t.Errorf("both selections: failure = %+v", failure)
	}

	var history historyDocument
	if err := decodeData([]byte(session.mustCall("tsipam-registry-history", map[string]any{
		"registry": url, "kind": "site_id", "limit": 1,
	})), &history); err != nil {
		t.Fatal(err)
	}
	if history.Events == nil || len(*history.Events) != 1 {
		t.Errorf("history = %+v, want the one reserve", history)
	}
	failure := session.mustFail("tsipam-registry-history", map[string]any{"registry-dir": dir})
	if failure.ErrorCode != codeValidationError || failure.ActionableFix != fixHistoryUnsupported {
		t.Errorf("history of a file registry: failure = %+v", failure)
	}
}

func TestMCPFailuresKeepTheServerRunning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet, writesRegistry)
	session := startMCP(t, nil)
	tests := []struct {
		tool, code string
		arguments  map[string]any
	}{
		{
			"tsipam-site-id-reserve",
			codeRegistryCollision,
			map[string]any{"id": "1", "site": "branch", "registry-dir": dir},
		},
		{"tsipam-site-id-reserve", codeValidationError, map[string]any{"id": "1", "registry-dir": dir}},
		{"tsipam-calc-split", codeValidationError, map[string]any{"cidr": "10.0.0.0/24"}},
		{"tsipam-calc-split", codeValidationError, map[string]any{"cidr": "10.0.0.0/24", "prefix": "bogus"}},
		{"tsipam-calc-info", codeValidationError, map[string]any{"cidr": "not-a-cidr"}},
		{"tsipam-calc-split", codeSplitTooLarge, map[string]any{"cidr": "2001:db8::/32", "prefix": 64}},
		{"tsipam-plan", codeCredentialsMissing, map[string]any{"registry-dir": dir}},
	}
	for _, test := range tests {
		failure := session.mustFail(test.tool, test.arguments)
		if failure.ErrorCode != test.code || failure.Version != resolvedVersion() {
			t.Errorf("%s %v: failure = %+v, want %s", test.tool, test.arguments, failure, test.code)
		}
	}
	session.mustCall("tsipam-site-id-list", map[string]any{"registry-dir": dir})
}

func TestMCPReleaseIsApprovedPerCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, localTailnet)
	const registryText = `{"siteIDs":{"1":"hq","2":"branch","3":"lab"}}`
	writeAggregateRegistry(t, dir, localTailnet, registryText)
	session := startMCP(t, nil)
	release := func(id string, extra map[string]any) map[string]any {
		arguments := map[string]any{"id": id, "registry-dir": dir}
		maps.Copy(arguments, extra)
		return arguments
	}
	registryBytes := func() string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	unapproved := func(id string, extra map[string]any) {
		t.Helper()
		before := registryBytes()
		failure := session.mustFail("tsipam-site-id-release", release(id, extra))
		if failure.ErrorCode != "confirmation_required" || registryBytes() != before {
			t.Errorf("failure = %+v, registry = %s; want a refusal and no change", failure, registryBytes())
		}
	}

	unapproved("1", nil)
	unapproved("1", map[string]any{"yes": false})
	for _, extra := range []map[string]any{{"dry-run": true}, {"dry-run": true, "yes": true}} {
		rehearsed := session.mustCall("tsipam-site-id-release", release("1", extra))
		if !strings.Contains(rehearsed, `"dry_run":true`) || registryBytes() != registryText {
			t.Errorf("rehearsal = %s, registry = %s", rehearsed, registryBytes())
		}
	}
	session.mustCall("tsipam-site-id-release", release("1", map[string]any{"yes": true}))
	if strings.Contains(registryBytes(), "hq") {
		t.Errorf("registry = %s, want ID 1 released", registryBytes())
	}
	unapproved("2", nil)
	committed := session.mustCall("tsipam-site-id-release", release("2", map[string]any{"yes": true}))
	if strings.Contains(committed, "dry_run") {
		t.Errorf("a rehearsal carried over into %s", committed)
	}
	unapproved("3", nil)
}

func TestMCPCallsShareNoRedactionState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tailnets := []string{"tailnet-alpha", "tailnet-beta"}
	for _, tailnet := range tailnets {
		writeAggregateRegistry(t, dir, tailnet, `{"siteIDs":{"1":"shared-secret-site"}}`)
	}
	env := map[string]string{sanitize.KeyEnv: testSanitizeKey}
	session := startMCP(t, env)
	commandLine := func(tailnet, level string) string {
		_, stdout, _ := runWith(t, env, commandSiteID, commandList,
			"--registry-dir", dir, "--tailnet", tailnet, "--sanitize", level)
		return maskRun(t, stdout)
	}
	for _, level := range []string{"identity", "off", "strict", "identity"} {
		for _, tailnet := range tailnets {
			got := maskRun(t, session.mustCall("tsipam-site-id-list", map[string]any{
				"registry-dir": dir, "tailnet": tailnet, "sanitize": level,
			}))
			if want := commandLine(tailnet, level); got != want {
				t.Errorf("%s at %s returned %s, want %s", tailnet, level, got, want)
			}
		}
	}
}

// TestMCPCannotLowerTheEnvironmentsLevel covers results, refusals, and arguments
// the parser rejects before any sanitizer exists.
func TestMCPCannotLowerTheEnvironmentsLevel(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "path-secret")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAggregateRegistry(t, dir, "tailnet-secret", `{"siteIDs":{"1":"hq-secret"}}`)
	session := startMCP(t, map[string]string{sanitize.LevelEnv: "identity"})
	base := map[string]any{"registry-dir": dir, "tailnet": "tailnet-secret", "sanitize": "off", "verbose": true}
	with := func(extra map[string]any) map[string]any {
		arguments := maps.Clone(base)
		maps.Copy(arguments, extra)
		return arguments
	}
	texts := map[string]string{"list": session.mustCall("tsipam-site-id-list", base)}
	for name, arguments := range map[string]map[string]any{
		"collision":  {"id": "1", "site": "branch-secret"},
		"invalid ID": {"id": "id-secret", "site": "hq"},
		"missing":    {"site": "branch-secret"},
		"bad bool":   {"id": "1", "site": "hq", "strict": "value-secret"},
	} {
		text, failed := session.call("tsipam-site-id-reserve", with(arguments))
		if !failed {
			t.Errorf("%s succeeded: %s", name, text)
		}
		texts[name] = text
	}
	texts["bad integer"], _ = session.call("tsipam-calc-split", map[string]any{
		"cidr": "10.0.0.0/24", "prefix": "value-secret", "sanitize": "off",
	})
	session.close()
	texts["server stderr"] = session.stderr.String()
	for name, text := range texts {
		if strings.Contains(text, "secret") {
			t.Errorf("%s exposes an identity: %s", name, text)
		}
	}
}

// TestMCPHidesArgumentsItCannotParse covers a call that asks for redaction in an
// argument the parser never reached.
func TestMCPHidesArgumentsItCannotParse(t *testing.T) {
	t.Parallel()
	session := startMCP(t, nil)
	// A successful unsanitized call first, whose level must not linger.
	session.mustCall("tsipam-calc-info", map[string]any{"cidr": "10.0.0.0/24"})
	failure := session.mustFail("tsipam-calc-split", map[string]any{
		"cidr": "10.0.0.0/24", "prefix": "value-secret", "sanitize": "identity",
	})
	if strings.Contains(failure.Message, "value-secret") {
		t.Errorf("message = %q, want the value hidden", failure.Message)
	}
}

// TestMCPUnknownArgumentHidesItsValue pins the accepted gap: the runtime refuses
// the call itself and repeats the name (rshade/ax-go#354), but not the value.
func TestMCPUnknownArgumentHidesItsValue(t *testing.T) {
	t.Parallel()
	session := startMCP(t, map[string]string{sanitize.LevelEnv: "identity"})
	failure := session.mustFail("tsipam-calc-info", map[string]any{
		"cidr": "10.0.0.0/24", "hq-secret": "branch-secret",
	})
	if failure.ErrorCode != codeValidationError || strings.Contains(failure.Message, "branch-secret") {
		t.Errorf("failure = %+v, want %s without the value", failure, codeValidationError)
	}
}

func TestMCPPolicyDriftCarriesItsCount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := policyFile(t, dir, `{"ipsets":{"ipset:lab":["10.0.0.0/24"]}}`)
	writeAggregateRegistry(t, dir, localTailnet, `{"subnetPools":{"10.9.0.0/16":"hq"}}`)
	session := startMCP(t, nil)
	failure := session.mustFail("tsipam-check-policy", map[string]any{"policy-file": policy, "registry-dir": dir})
	if failure.ErrorCode != codePolicyDriftDetected || failure.Context[contextDriftCount] != float64(2) {
		t.Errorf("failure = %+v, want %s with a count of 2", failure, codePolicyDriftDetected)
	}
}

func TestMCPCallContinuesTheClientsTrace(t *testing.T) {
	t.Parallel()
	session := startMCP(t, nil)
	raw := session.request("tools/call", map[string]any{
		"name": "tsipam-calc-info", "arguments": map[string]any{"cidr": "10.0.0.0/24"},
		"_meta": map[string]any{"traceparent": testTraceParent},
	})
	if !strings.Contains(string(raw), testTraceID) {
		t.Errorf("result = %s, want trace %s", raw, testTraceID)
	}
}
