// Command user lets an Operator create Users and reset their passwords:
//
//	user create <email>
//	user reset-password <email>
//
// The password is read from a no-echo prompt (with confirmation) on a
// terminal, otherwise from one line of stdin. Never from a flag or env var.
// Exit status: 0 success, 1 failure (nothing written), 2 usage error.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"
)

const usage = "usage: user create <email>\n       user reset-password <email>\n"

func main() {
	// Usage errors need no database, so they are reported before app.Run.
	if _, _, ok := parseArgs(os.Args[1:]); !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	app.Run("user", func(ctx context.Context, cfg config.Config, _ *slog.Logger, pool *pgxpool.Pool) error { // pageliterals:ok command name, not a Page origin
		c := &cli{
			pool: pool,
			hasher: user.NewHasher(user.Params{
				MemoryKiB: cfg.Argon2MemoryKiB, Time: cfg.Argon2Time, Threads: cfg.Argon2Threads,
			}),
		}
		if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
			c.secret = func(prompt string) (string, error) {
				fmt.Fprint(os.Stderr, prompt)
				b, err := term.ReadPassword(fd)
				fmt.Fprintln(os.Stderr)
				return string(b), err
			}
		}
		if code := c.run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
			pool.Close()
			os.Exit(code)
		}
		return nil
	})
}

type cli struct {
	pool   *pgxpool.Pool
	hasher *user.Hasher
	// secret reads a password without echo; nil means stdin is not a terminal.
	secret func(prompt string) (string, error)
}

func parseArgs(args []string) (cmd, email string, ok bool) {
	if len(args) != 2 || (args[0] != "create" && args[0] != "reset-password") || strings.HasPrefix(args[1], "-") {
		return "", "", false
	}
	return args[0], args[1], true
}

// run executes one command and returns the process exit status.
func (c *cli) run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd, email, ok := parseArgs(args)
	if !ok {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fail := func(msg string, a ...any) int {
		fmt.Fprintf(stderr, "user: "+msg+"\n", a...)
		return 1
	}
	password, err := c.readPassword(stdin)
	if err != nil {
		return fail("%v", err)
	}
	// Display the normalised email; an invalid one is reported by the call below.
	shown := email
	if n, err := user.NormalizeEmail(email); err == nil {
		shown = n
	}

	switch cmd {
	case "create":
		_, err = user.Create(ctx, c.pool, c.hasher, email, password)
		switch {
		case errors.Is(err, user.ErrEmailTaken):
			return fail("a User with email %s already exists", shown)
		case err != nil:
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "user: created %s\n", shown)
	default:
		n, err := user.ResetPassword(ctx, c.pool, c.hasher, email, password)
		switch {
		case errors.Is(err, user.ErrNotFound):
			return fail("no User with email %s", shown)
		case err != nil:
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "user: reset password for %s, signed out %d sessions\n", shown, n)
	}
	return 0
}

// readPassword prompts twice on a terminal, or takes one stdin line with
// only the trailing newline stripped.
func (c *cli) readPassword(stdin io.Reader) (string, error) {
	if c.secret != nil {
		first, err := c.secret("Password: ")
		if err != nil {
			return "", err
		}
		second, err := c.secret("Confirm password: ")
		if err != nil {
			return "", err
		}
		if first != second {
			return "", errors.New("passwords do not match")
		}
		return first, nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}
