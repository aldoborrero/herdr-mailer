package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aldoborrero/herdr-mailer/internal/daemon"
	"github.com/aldoborrero/herdr-mailer/internal/identity"
	"github.com/aldoborrero/herdr-mailer/internal/store"
	"github.com/aldoborrero/herdr-mailer/internal/transport"
	"github.com/spf13/cobra"
)

func DefaultState() string {
	if s := os.Getenv("HERDR_MAILER_STATE"); s != "" {
		return s
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "herdr-mailer")
}
func New(out, errOut io.Writer) *cobra.Command {
	var state, credential string
	var jsonOutput bool
	root := &cobra.Command{Use: "herdr-mailer", Short: "Durable local agent messaging", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVar(&state, "state", DefaultState(), "private application state directory")
	root.PersistentFlags().StringVar(&credential, "credential", os.Getenv("HERDR_MAILER_CREDENTIAL"), "worker credential file (token only; never owner.json)")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "emit JSON (also the default in v0.1)")
	token := func() (string, error) {
		if credential == "" {
			return "", fmt.Errorf("set --credential or HERDR_MAILER_CREDENTIAL")
		}
		b, e := os.ReadFile(credential)
		return strings.TrimSpace(string(b)), e
	}
	call := func(cmd *cobra.Command, r daemon.Request) error {
		t, e := token()
		if e != nil {
			return e
		}
		r.Token = t
		b, e := daemon.Call(cmd.Context(), state, r, nil)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, string(b))
		return e
	}
	root.AddCommand(&cobra.Command{Use: "init", Short: "Bootstrap private owner identity and administrator credential", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, e := identity.Bootstrap(state)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]string{"owner": o.DID, "state": state})
	}})
	var runtime, location, output string
	register := &cobra.Command{Use: "register <agent>", Short: "Owner-only session registration; rotates token and epoch", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if output == "" {
			return fmt.Errorf("--output credential file is required")
		}
		f, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		ok := false
		defer func() {
			f.Close()
			if !ok {
				os.Remove(output)
			}
		}()
		o, e := identity.Load(state)
		if e != nil {
			return e
		}
		data, e := daemon.Call(cmd.Context(), state, daemon.Request{Op: "register", Token: o.AdminToken, Agent: args[0], Runtime: runtime, Location: location}, nil)
		if e != nil {
			return e
		}
		var resp struct {
			Session json.RawMessage `json:"session"`
			Token   string          `json:"token"`
		}
		if e = json.Unmarshal(data, &resp); e != nil {
			return e
		}
		if _, e = f.WriteString(resp.Token + "\n"); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		ok = true
		_, e = fmt.Fprintln(out, string(resp.Session))
		return e
	}}
	register.Flags().StringVar(&runtime, "runtime", "none", "codex, pi, claude, or none")
	register.Flags().StringVar(&location, "location", "", "exact runtime session UUID (required except for none)")
	register.Flags().StringVar(&output, "output", "", "new private token file; fails if it already exists")
	root.AddCommand(register)
	var nc transport.Config
	serve := &cobra.Command{Use: "daemon", Short: "Serve private Unix socket and durable delivery workers", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o, e := identity.Load(state)
		if e != nil {
			return e
		}
		db, e := store.Open(state, o.DID)
		if e != nil {
			return e
		}
		defer db.Close()
		s := &daemon.Server{Store: db, Admin: o.AdminToken}
		if nc.URL != "" {
			s.Broker = &nc
		}
		return s.Serve(cmd.Context(), state)
	}}
	serve.Flags().StringVar(&nc.URL, "nats-url", "", "optional private NATS URL (disabled by default)")
	serve.Flags().StringVar(&nc.Stream, "nats-stream", "HERDR_MAILER", "private JetStream stream")
	serve.Flags().StringVar(&nc.Prefix, "nats-prefix", "herdr_mailer", "private subject prefix")
	serve.Flags().StringVar(&nc.Credentials, "nats-credentials", "", "NATS credentials file for trusted bridge")
	root.AddCommand(serve)
	for _, op := range []string{"send", "ask", "reply"} {
		var ttl string
		var askAuthor, operationID, recipientSession string
		var recipientEpoch int64
		target := "recipient"
		if op == "reply" {
			target = "ask-id"
		}
		c := &cobra.Command{Use: op + " <" + target + "> <body>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			r := daemon.Request{Op: cmd.Name(), Recipient: args[0], Body: args[1], TTL: ttl, OperationID: operationID, RecipientSession: recipientSession, RecipientEpoch: recipientEpoch}
			if cmd.Name() == "reply" {
				r.ID = args[0]
				r.Recipient = ""
				r.Author = askAuthor
			}
			return call(cmd, r)
		}}
		c.Flags().StringVar(&operationID, "operation-id", "", "stable retry key scoped to the logical author")
		c.Flags().StringVar(&recipientSession, "recipient-session", "", "require exact recipient mailer session (paired with epoch)")
		c.Flags().Int64Var(&recipientEpoch, "recipient-epoch", 0, "require exact recipient epoch (paired with session)")
		c.Flags().StringVar(&ttl, "ttl", "24h", "delivery expiry (maximum 720h)")
		if op == "reply" {
			c.Flags().StringVar(&askAuthor, "author", "", "original ask author when an ID is ambiguous")
		}
		root.AddCommand(c)
	}
	var unread bool
	c := &cobra.Command{Use: "inbox", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, daemon.Request{Op: "inbox", Unread: unread})
	}}
	c.Flags().BoolVar(&unread, "unread", false, "only unread messages")
	root.AddCommand(c)
	var readAuthor string
	read := &cobra.Command{Use: "read <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, daemon.Request{Op: "read", ID: args[0], Author: readAuthor})
	}}
	read.Flags().StringVar(&readAuthor, "author", "", "message author when an ID is ambiguous")
	root.AddCommand(read)
	for _, op := range []string{"status", "doctor", "capabilities"} {
		root.AddCommand(&cobra.Command{Use: op, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return call(cmd, daemon.Request{Op: cmd.Name()}) }})
	}
	var follow bool
	var cursor int64
	events := &cobra.Command{Use: "events", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		t, e := token()
		if e != nil {
			return e
		}
		_, e = daemon.Call(cmd.Context(), state, daemon.Request{Op: "events", Token: t, Cursor: cursor, Follow: follow}, func(b json.RawMessage) error { _, e := fmt.Fprintln(out, string(b)); return e })
		return e
	}}
	events.Flags().BoolVar(&follow, "follow", false, "wait on live recipient events until cancelled")
	events.Flags().Int64Var(&cursor, "cursor", 0, "resume after durable event cursor")
	root.AddCommand(events)
	return root
}
func Run(ctx context.Context, args []string, out, errOut io.Writer) error {
	root := New(out, errOut)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}
