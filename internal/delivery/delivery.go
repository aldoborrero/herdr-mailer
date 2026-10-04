package delivery

import (
	"context"
	"errors"
	"fmt"
	"github.com/aldoborrero/herdr-mailer/internal/record"
	"os/exec"
	"time"
)

var ErrUnsupported = errors.New("unsupported runtime")
var ErrAmbiguous = errors.New("queue outcome ambiguous")

type Adapter interface {
	Deliver(context.Context, record.Session, record.Message) error
}
type Runner func(context.Context, string, ...string) error
type Codex struct {
	Executable string
	Run        Runner
}

func (c Codex) Deliver(ctx context.Context, s record.Session, m record.Message) error {
	if s.Runtime != "codex" {
		return ErrUnsupported
	}
	if !record.ValidUUID(s.Location) {
		return fmt.Errorf("invalid session UUID")
	}
	if !time.Now().Before(m.ExpiresAt) {
		return context.DeadlineExceeded
	}
	deadline := time.Now().Add(15 * time.Second)
	if m.ExpiresAt.Before(deadline) {
		deadline = m.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	run := c.Run
	if run == nil {
		run = func(ctx context.Context, exe string, args ...string) error {
			return exec.CommandContext(ctx, exe, args...).Run()
		}
	}
	exe := c.Executable
	if exe == "" {
		exe = "codex"
	}
	text := fmt.Sprintf("[herdr-mailer %s; %s from %s; correlation=%s]\n%s", m.ID, m.Kind, m.Author, m.Correlation, m.Body)
	err := run(ctx, exe, "queue", "--thread", s.Location, "--message", text)
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ErrAmbiguous, ctx.Err())
	}
	return err
}
