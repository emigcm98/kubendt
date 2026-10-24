package executor

import (
	"fmt"
	"strings"
)

type CommandKind string

const (
	CommandKindArgs  CommandKind = "args"
	CommandKindLine  CommandKind = "line"
	CommandKindBatch CommandKind = "batch"
)

// Command is the canonical execution unit used by executors.
// - args: tokenized command (best for linux-like shells/binaries)
// - line: raw single-line command (best for CLIs such as xr_cli)
// - batch: several lines fed to one program over stdin (tc -b). Line holds
// the program name for display, Lines the commands without it.
type Command struct {
	Kind  CommandKind
	Args  []string
	Line  string
	Lines []string
	// Container names where a kubectl command runs, the pod's main
	// container when empty; the helpers point it at the toolbox for a pod
	// whose image lacks the tool.
	Container string
}

func NewArgsCommand(args []string) Command {
	copied := make([]string, len(args))
	copy(copied, args)
	return Command{Kind: CommandKindArgs, Args: copied}
}

func NewLineCommand(line string) Command {
	return Command{Kind: CommandKindLine, Line: strings.TrimSpace(line)}
}

// NewBatchCommand groups lines for one program's batch mode. program is only
// shown back to the user, each line is run as "<program> <line>".
func NewBatchCommand(program string, lines []string) Command {
	copied := make([]string, 0, len(lines))
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			copied = append(copied, t)
		}
	}
	return Command{Kind: CommandKindBatch, Line: program, Lines: copied}
}

func (c Command) String() string {
	switch c.Kind {
	case CommandKindLine:
		return c.Line
	case CommandKindArgs:
		return strings.Join(c.Args, " ")
	case CommandKindBatch:
		return c.Line + " " + strings.Join(c.Lines, " && "+c.Line+" ")
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
	case CommandKindBatch:
		if len(c.Lines) == 0 {
			return fmt.Errorf("batch command cannot be empty")
		}
		return nil
	default:
		return fmt.Errorf("unknown command kind %q", c.Kind)
	}
}

// CommandKinder is implemented by executors whose commands are not plain
// argv rows: a CLI that takes the whole command as one line, or a program
// run in batch mode. Everything else takes args.
type CommandKinder interface {
	CommandKind() CommandKind
}

// KindFor returns the command kind an executor takes.
func KindFor(executorName string) CommandKind {
	if e, err := Get(strings.TrimSpace(executorName)); err == nil {
		if k, ok := e.(CommandKinder); ok {
			return k.CommandKind()
		}
	}
	return CommandKindArgs
}

// CommandsFromLegacy converts the historical [][]string representation into the
// new typed command model.
func CommandsFromLegacy(commands [][]string) []Command {
	return CommandsFromLegacyForExecutor(commands, DefaultExecutorName)
}

// CommandsFromLegacyForExecutor converts the historical [][]string
// representation into the typed command model of a specific executor, in
// the kind that executor declares.
func CommandsFromLegacyForExecutor(commands [][]string, executorName string) []Command {
	switch KindFor(executorName) {
	case CommandKindBatch:
		return []Command{batchFromRows(commands)}
	case CommandKindLine:
		out := make([]Command, 0, len(commands))
		for _, c := range commands {
			out = append(out, NewLineCommand(strings.Join(c, " ")))
		}
		return out
	default:
		out := make([]Command, 0, len(commands))
		for _, c := range commands {
			out = append(out, NewArgsCommand(c))
		}
		return out
	}
}

// batchFromRows turns the rows of one action, all invocations of the same
// program (["tc", "qdisc", ...] each), into a single batch for that program,
// so the action costs one exec whatever its size: a netem is two lines, an
// htb tree with filters a dozen.
func batchFromRows(rows [][]string) Command {
	program := ""
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if program == "" {
			program = row[0]
		}
		if row[0] == program {
			row = row[1:]
		}
		lines = append(lines, strings.Join(row, " "))
	}
	return NewBatchCommand(program, lines)
}
