package executor

import (
	"fmt"
	"strings"
)

type CommandKind string

const (
	CommandKindArgs CommandKind = "args"
	CommandKindLine CommandKind = "line"
)

// Command is the canonical execution unit used by executors.
// - args: tokenized command (best for linux-like shells/binaries)
// - line: raw single-line command (best for CLIs such as xr_cli)
type Command struct {
	Kind CommandKind
	Args []string
	Line string
}

func NewArgsCommand(args []string) Command {
	copied := make([]string, len(args))
	copy(copied, args)
	return Command{Kind: CommandKindArgs, Args: copied}
}

func NewLineCommand(line string) Command {
	return Command{Kind: CommandKindLine, Line: strings.TrimSpace(line)}
}

func (c Command) String() string {
	switch c.Kind {
	case CommandKindLine:
		return c.Line
	case CommandKindArgs:
		return strings.Join(c.Args, " ")
	default:
		return ""
	}
}

func (c Command) Validate() error {
	switch c.Kind {
	case CommandKindArgs:
		if len(c.Args) == 0 {
			return fmt.Errorf("args command cannot be empty")
		}
		return nil
	case CommandKindLine:
		if strings.TrimSpace(c.Line) == "" {
			return fmt.Errorf("line command cannot be empty")
		}
		return nil
	default:
		return fmt.Errorf("unknown command kind %q", c.Kind)
	}
}

// CommandsFromLegacy converts the historical [][]string representation into the
// new typed command model.
func CommandsFromLegacy(commands [][]string) []Command {
	return CommandsFromLegacyForExecutor(commands, DefaultExecutorName)
}

// CommandsFromLegacyForExecutor converts the historical [][]string
// representation into the new typed command model for a specific executor.
//
// For xr_cli we force line commands explicitly because xr_cli expects one
// string argument representing the whole CLI command.
func CommandsFromLegacyForExecutor(commands [][]string, executorName string) []Command {
	out := make([]Command, 0, len(commands))
	useLine := strings.TrimSpace(executorName) == XRCLIExecutorName
	for _, c := range commands {
		if useLine {
			out = append(out, NewLineCommand(strings.Join(c, " ")))
			continue
		}
		out = append(out, NewArgsCommand(c))
	}
	return out
}
