package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/october-dev/october-bus/bus"
)

const usage = `October Bus

Usage:
  october-bus start [--port <port>]
  october-bus stop
  october-bus status
  october-bus doctor [--json]
  october-bus scope create [scope-id]
  october-bus message receipt <message-id> [--json] [--address <addr>]
  october-bus agent list [--json] [--address <addr>]
  october-bus agent run --id <id> --name <name> [--connect-to <peer>] -- <command> [args...]
  october-bus mcp stdio
  october-bus demo
  october-bus version
`

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func start(args []string) error {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	port := flags.Int("port", 0, "loopback port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *port < 0 || *port > 65535 {
		return errors.New("port must be between 0 and 65535")
	}
	daemon, err := bus.StartDaemon(context.Background(), *port, nil)
	if err != nil {
		return err
	}
	fmt.Printf("October Bus listening on %s\n", daemon.RunFile.Address)
	fmt.Printf("Run file: %s\n", daemon.Paths.RunFile)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var serveErr error
	select {
	case <-ctx.Done():
	case <-daemon.Server.ShutdownRequested():
	case serveErr = <-daemon.Server.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopErr := daemon.Stop(shutdown)
	if serveErr != nil {
		return fmt.Errorf("October Bus stopped unexpectedly: %w", serveErr)
	}
	return stopErr
}

func stop() error {
	paths, err := bus.DefaultDaemonPaths()
	if err != nil {
		return err
	}
	run, err := bus.ReadRunFile(paths.RunFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (bus.Client{Address: run.Address, Token: run.AdminToken}).Shutdown(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(paths.RunFile); errors.Is(err, os.ErrNotExist) {
			fmt.Println("October Bus stopped")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("October Bus did not stop within 10 seconds")
}

type diagnosticReport struct {
	Healthy          bool   `json:"healthy"`
	RuntimeVersion   string `json:"runtimeVersion"`
	ProtocolVersion  string `json:"protocolVersion"`
	Address          string `json:"address,omitempty"`
	PID              int    `json:"pid,omitempty"`
	DataDirectory    string `json:"dataDirectory"`
	RuntimeDirectory string `json:"runtimeDirectory"`
	DatabaseExists   bool   `json:"databaseExists"`
	RunFileExists    bool   `json:"runFileExists"`
	Problem          string `json:"problem,omitempty"`
}

func doctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	paths, err := bus.DefaultDaemonPaths()
	if err != nil {
		return err
	}
	report := diagnosticReport{
		RuntimeVersion: bus.Version, ProtocolVersion: bus.ProtocolVersion,
		DataDirectory: paths.DataDir, RuntimeDirectory: paths.RuntimeDir,
	}
	if _, err := os.Stat(paths.Database); err == nil {
		report.DatabaseExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		report.Problem = err.Error()
	}
	if _, err := os.Stat(paths.RunFile); err == nil {
		report.RunFileExists = true
	} else if !errors.Is(err, os.ErrNotExist) && report.Problem == "" {
		report.Problem = err.Error()
	}
	if report.RunFileExists {
		run, readErr := bus.ReadRunFile(paths.RunFile)
		if readErr != nil {
			report.Problem = readErr.Error()
		} else {
			report.Address, report.PID = run.Address, run.PID
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			health, healthErr := (bus.Client{Address: run.Address}).Health(ctx)
			cancel()
			if healthErr != nil {
				report.Problem = healthErr.Error()
			} else if health.ProtocolVersion != run.ProtocolVersion {
				report.Problem = "run file and daemon protocol versions differ"
			} else {
				report.Healthy = health.Status == "ready"
				report.RuntimeVersion = health.RuntimeVersion
				report.ProtocolVersion = health.ProtocolVersion
			}
		}
	} else if report.Problem == "" {
		report.Problem = "October Bus is not running"
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
	} else {
		state := "not ready"
		if report.Healthy {
			state = "ready"
		}
		fmt.Printf("October Bus is %s\n", state)
		fmt.Printf("Runtime %s, protocol %s\n", report.RuntimeVersion, report.ProtocolVersion)
		fmt.Printf("Data: %s\n", report.DataDirectory)
		fmt.Printf("Runtime: %s\n", report.RuntimeDirectory)
		if report.Address != "" {
			fmt.Printf("Endpoint: %s, pid %d\n", report.Address, report.PID)
		}
		if report.Problem != "" {
			fmt.Printf("Problem: %s\n", report.Problem)
		}
	}
	if !report.Healthy {
		return errors.New("October Bus diagnostics did not pass")
	}
	return nil
}

func status() error {
	paths, err := bus.DefaultDaemonPaths()
	if err != nil {
		return err
	}
	run, err := bus.ReadRunFile(paths.RunFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	health, err := (bus.Client{Address: run.Address}).Health(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("October Bus is %s at %s\n", health.Status, run.Address)
	fmt.Printf("Runtime %s, protocol %s, pid %d\n", health.RuntimeVersion, health.ProtocolVersion, run.PID)
	return nil
}

func createScope(id string) error {
	paths, err := bus.DefaultDaemonPaths()
	if err != nil {
		return err
	}
	run, err := bus.ReadRunFile(paths.RunFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := (bus.Client{Address: run.Address, Token: run.AdminToken}).CreateScope(ctx, bus.CreateScopeInput{ID: id})
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

// resolveBusAddress returns the daemon address to talk to for a CLI subcommand
// that uses an agent credential. Precedence: explicit flag, then the
// OCTOBER_BUS_ADDRESS environment variable, then the daemon run file.
func resolveBusAddress(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv("OCTOBER_BUS_ADDRESS"); env != "" {
		return env, nil
	}
	paths, err := bus.DefaultDaemonPaths()
	if err != nil {
		return "", err
	}
	run, err := bus.ReadRunFile(paths.RunFile)
	if err != nil {
		return "", err
	}
	return run.Address, nil
}

// inspectReceipt implements `october-bus message receipt <message-id>`.
//
// It reads the agent credential from OCTOBER_BUS_AGENT_TOKEN and fetches the
// delivery receipt through the public client API. The receipt deliberately
// exposes only delivery state and timestamps; message bodies and shared
// context are never included.
func inspectReceipt(args []string) error {
	flags := flag.NewFlagSet("message receipt", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	address := flags.String("address", "", "October Bus address")
	if err := flags.Parse(receiptFlagArgs(args)); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 1 {
		return errors.New("message receipt requires a single <message-id> argument")
	}
	messageID := strings.TrimSpace(positional[0])
	if messageID == "" {
		return errors.New("message id must not be empty")
	}
	token := os.Getenv("OCTOBER_BUS_AGENT_TOKEN")
	if token == "" {
		return errors.New("OCTOBER_BUS_AGENT_TOKEN is required")
	}
	resolved, err := resolveBusAddress(*address)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, err := (bus.Client{Address: resolved, Token: token}).Receipt(ctx, messageID)
	if err != nil {
		return fmt.Errorf("could not inspect message receipt: %w", err)
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	return printReceiptHuman(receipt)
}

// receiptFlagArgs moves supported flags before the message id so the command
// accepts flags on either side of its single positional argument. The standard
// flag package otherwise stops parsing at the first positional argument.
func receiptFlagArgs(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			positional = append(positional, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positional = append(positional, argument)
			continue
		}
		flags = append(flags, argument)
		if (argument == "-address" || argument == "--address") && index+1 < len(args) {
			index++
			flags = append(flags, args[index])
		}
	}
	return append(flags, positional...)
}

// printReceiptHuman renders a DeliveryReceipt as a stable, multi-line summary.
// Only state, timestamps, and the linked response message ID are shown; the
// receipt struct contains no body or shared-context fields, so this view
// cannot leak message contents.
func printReceiptHuman(receipt bus.DeliveryReceipt) error {
	fmt.Printf("Message %s\n", receipt.MessageID)
	fmt.Printf("State: %s\n", receipt.State)
	for _, ts := range []struct {
		label string
		value string
	}{
		{"AcceptedAt", receipt.AcceptedAt},
		{"DeliveredAt", receipt.DeliveredAt},
		{"AcknowledgedAt", receipt.AcknowledgedAt},
		{"RepliedAt", receipt.RepliedAt},
	} {
		if ts.value != "" {
			fmt.Printf("%s: %s\n", ts.label, ts.value)
		}
	}
	if receipt.ResponseMessageID != "" {
		fmt.Printf("ResponseMessageID: %s\n", receipt.ResponseMessageID)
	}
	return nil
}

// listAgents implements `october-bus agent list [--json] [--address <addr>]`.
// It reads the scope credential from OCTOBER_BUS_SCOPE_TOKEN and returns only
// the agent metadata visible to that scope.
func listAgents(args []string) error {
	flags := flag.NewFlagSet("agent list", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	address := flags.String("address", "", "October Bus address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("agent list does not accept positional arguments")
	}
	token := os.Getenv("OCTOBER_BUS_SCOPE_TOKEN")
	if token == "" {
		return errors.New("OCTOBER_BUS_SCOPE_TOKEN is required")
	}
	resolved, err := resolveBusAddress(*address)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agents, err := (bus.Client{Address: resolved, Token: token}).ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("could not list agents: %w", err)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	if *jsonOutput {
		encoded, err := json.MarshalIndent(agents, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	return printAgentsHuman(agents)
}

// printAgentsHuman renders a stable summary. Display names are quoted because
// they are user-controlled text and may contain terminal control characters.
func printAgentsHuman(agents []bus.Agent) error {
	if len(agents) == 0 {
		fmt.Println("No agents in scope.")
		return nil
	}
	for _, agent := range agents {
		fmt.Printf("%s (%q)\n", agent.ID, agent.DisplayName)
		fmt.Printf("  lifecycle:  %s\n", agent.Lifecycle)
		fmt.Printf("  ready:      %s\n", yesNo(agent.Ready))
		fmt.Printf("  reachable:  %s\n", yesNo(agent.Reachable))
		if len(agent.Capabilities) == 0 {
			fmt.Println("  capabilities: (none)")
		} else {
			names := make([]string, 0, len(agent.Capabilities))
			for _, capability := range agent.Capabilities {
				names = append(names, capability.Name)
			}
			fmt.Printf("  capabilities: %s\n", strings.Join(names, ", "))
		}
		if agent.UpdatedAt != "" {
			fmt.Printf("  updatedAt:  %s\n", agent.UpdatedAt)
		}
	}
	return nil
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func setEnvironment(base []string, values ...string) []string {
	replacements := map[string]bool{}
	for i := 0; i < len(values); i += 2 {
		replacements[strings.ToUpper(values[i])] = true
	}
	result := make([]string, 0, len(base)+len(values)/2)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !replacements[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	for i := 0; i < len(values); i += 2 {
		result = append(result, values[i]+"="+values[i+1])
	}
	return result
}

func removeEnvironment(base []string, names ...string) []string {
	removed := map[string]bool{}
	for _, name := range names {
		removed[strings.ToUpper(name)] = true
	}
	result := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !removed[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	return result
}

func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	id := flags.String("id", "", "stable agent id")
	name := flags.String("name", "", "agent display name")
	address := flags.String("address", "", "October Bus address")
	scopeTokenEnv := flags.String("scope-token-env", "OCTOBER_BUS_SCOPE_TOKEN", "environment variable containing the scope token")
	lease := flags.Duration("lease", 5*time.Minute, "execution lease")
	heartbeat := flags.Duration("heartbeat", 0, "heartbeat interval")
	var connectTo stringList
	var capabilities stringList
	flags.Var(&connectTo, "connect-to", "agent id to link, repeatable")
	flags.Var(&capabilities, "capability", "capability name, repeatable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	commandArgs := flags.Args()
	if *id == "" || *name == "" || len(commandArgs) == 0 {
		return errors.New("agent run requires --id, --name, and a command after --")
	}
	if *scopeTokenEnv == "" {
		return errors.New("scope token environment variable name is required")
	}
	scopeToken := os.Getenv(*scopeTokenEnv)
	if scopeToken == "" {
		return fmt.Errorf("%s is required", *scopeTokenEnv)
	}
	resolvedAddress := *address
	if resolvedAddress == "" {
		resolvedAddress = os.Getenv("OCTOBER_BUS_ADDRESS")
	}
	if resolvedAddress == "" {
		paths, err := bus.DefaultDaemonPaths()
		if err != nil {
			return err
		}
		run, err := bus.ReadRunFile(paths.RunFile)
		if err != nil {
			return err
		}
		resolvedAddress = run.Address
	}
	declaredCapabilities := make([]bus.AgentCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		declaredCapabilities = append(declaredCapabilities, bus.AgentCapability{Name: capability})
	}
	session, err := bus.StartAgentSession(context.Background(), bus.AgentSessionOptions{
		Address: resolvedAddress, ScopeToken: scopeToken, HeartbeatInterval: *heartbeat,
		Registration: bus.RegisterAgentInput{
			ID: *id, DisplayName: *name, ConnectTo: connectTo,
			Capabilities: declaredCapabilities, LeaseMS: lease.Milliseconds(),
		},
	})
	if err != nil {
		return err
	}
	closeSession := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return session.Close(ctx)
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	childCtx, stopChild := context.WithCancel(context.Background())
	defer stopChild()
	command := exec.CommandContext(childCtx, commandArgs[0], commandArgs[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = setEnvironment(removeEnvironment(os.Environ(), *scopeTokenEnv),
		"OCTOBER_BUS_ADDRESS", resolvedAddress,
		"OCTOBER_BUS_MCP_URL", resolvedAddress+"/mcp",
		"OCTOBER_BUS_AGENT_ID", session.Registration.AgentID,
		"OCTOBER_BUS_EXECUTION_ID", session.Registration.ExecutionID,
		"OCTOBER_BUS_AGENT_TOKEN", session.Registration.AgentToken,
	)
	if err := command.Start(); err != nil {
		_ = closeSession()
		return err
	}
	fmt.Fprintf(os.Stderr, "Agent %s connected to %s\n", session.Registration.AgentID, resolvedAddress)
	childDone := make(chan error, 1)
	go func() { childDone <- command.Wait() }()

	select {
	case childErr := <-childDone:
		if closeErr := closeSession(); childErr == nil {
			return closeErr
		}
		return childErr
	case <-session.Done():
		stopChild()
		<-childDone
		if sessionErr := session.Err(); sessionErr != nil {
			_ = closeSession()
			return fmt.Errorf("agent session ended: %w", sessionErr)
		}
		return closeSession()
	case <-signalCtx.Done():
		stopChild()
		<-childDone
		return closeSession()
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "start":
		return start(args[1:])
	case "stop":
		return stop()
	case "status":
		return status()
	case "doctor":
		return doctor(args[1:])
	case "scope":
		if len(args) >= 2 && args[1] == "create" {
			id := ""
			if len(args) >= 3 {
				id = args[2]
			}
			return createScope(id)
		}
	case "message":
		if len(args) >= 2 && args[1] == "receipt" {
			return inspectReceipt(args[2:])
		}
	case "agent":
		if len(args) >= 2 && args[1] == "run" {
			return runAgent(args[2:])
		}
		if len(args) >= 2 && args[1] == "list" {
			return listAgents(args[2:])
		}
	case "mcp":
		if len(args) == 2 && args[1] == "stdio" {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return runMCPStdio(ctx)
		}
	case "demo":
		return bus.RunDemo(context.Background())
	case "version":
		fmt.Printf("october-bus %s (protocol %s)\n", bus.Version, bus.ProtocolVersion)
		return nil
	}
	return fmt.Errorf("unknown command: %v\n\n%s", args, usage)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
