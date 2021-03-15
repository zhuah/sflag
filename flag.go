package sflag

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

var ErrHelp = flag.ErrHelp

type UsageFunc func(printDefaults func(w io.Writer))

type CommandResolveFunc func(args []string, commands []Command) ([]string, bool)

type Command struct {
	Name  string
	Usage string

	// global flags must not be nil with this option
	RunWithFlags func(flags interface{}, args []string)
	// global flags will be ignored even if not nil
	Run func(args []string)
}

type flagInfo struct {
	Name    string
	Env     string
	Default string
	Usage   string
	Type    string

	NonFlag      bool
	NonFlagSlice bool
}

type commandFlags struct {
	name           string
	flags          []flagInfo
	stringNonFlags []flagInfo
	sliceNonFlag   []flagInfo

	subcommands []Command

	usage UsageFunc
}

func (c *commandFlags) printDefaults(w io.Writer) {
	if len(c.flags)+len(c.stringNonFlags)+len(c.sliceNonFlag)+len(c.subcommands) == 0 {
		fprintln(w, "no options.")
		return
	}
	tw := tabWriter(w, 2)

	hasFlag := len(c.flags) > 0 || len(c.stringNonFlags) > 0 || len(c.sliceNonFlag) > 0
	if hasFlag || len(c.subcommands) > 0 {
		fprintf(tw, "Usage: %s", c.name)
		if len(c.flags) == 1 {
			fprintf(tw, " [OPTION]")
		} else if len(c.flags) > 1 {
			fprintf(tw, " [OPTION]...")
		}
		for _, f := range c.stringNonFlags {
			fprintf(tw, " %s", f.Name)
		}
		for _, f := range c.sliceNonFlag {
			fprintf(tw, " %s...", f.Name)
		}
		if len(c.subcommands) > 0 {
			fprintf(tw, " COMMAND [ARGUMENT]...")
		}
		fprintf(tw, "\n")
	} else {
		fprintf(tw, "Usage of %s:\n", c.name)
	}
	if hasFlag {
		fprintf(tw, "\nOptions:\n")
		for _, fs := range [][]flagInfo{c.flags, c.stringNonFlags, c.sliceNonFlag} {
			for _, f := range fs {
				fprintf(tw, "\t%s\t%s", f.Name, f.Type)
				if f.Default != "" || f.Env != "" {
					fprintf(tw, ` (`)
					if f.Default != "" {
						fprintf(tw, `default: %s`, f.Default)
					}
					if f.Env != "" {
						if f.Default != "" {
							fprintf(tw, `, `)
						}
						fprintf(tw, `env: %s`, f.Env)
					}

					fprintf(tw, `)`)
				}
				fprintln(tw)
				if f.Usage != "" {
					fprintf(tw, "\t\t%s\n", f.Usage)
				}
			}
		}
	}
	if len(c.subcommands) > 0 {
		fprintf(tw, "\nCommands:\n")
		for _, cmd := range c.subcommands {
			fprintf(tw, "\t%s\t%s\n", cmd.Name, cmd.Usage)
		}
	}
	_ = tw.Flush()
}

func (c *commandFlags) printHelp() {
	if c.usage == nil {
		c.printDefaults(os.Stderr)
	} else {
		c.usage(c.printDefaults)
	}
}

var flagValueType = reflect.TypeOf((*flag.Value)(nil)).Elem()

type commonflagValue struct {
	val reflect.Value
}

var _ flag.Value = &commonflagValue{}

func (p *commonflagValue) IsBoolFlag() bool {
	return p.val.Kind() == reflect.Bool
}

func (p *commonflagValue) String() string {
	switch p.val.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(p.val.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(p.val.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(p.val.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(p.val.Float(), 'f', -1, 64)
	case reflect.String:
		return p.val.String()
	}
	panic("unreachable")
}

func (p *commonflagValue) Set(s string) error {
	switch p.val.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		p.val.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, int(p.val.Type().Size()*8))
		if err != nil {
			return err
		}
		p.val.SetInt(v)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, int(p.val.Type().Size()*8))
		if err != nil {
			return err
		}
		p.val.SetUint(v)
		return nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(s, int(p.val.Type().Size()*8))
		if err != nil {
			return err
		}
		p.val.SetFloat(v)
		return nil
	case reflect.String:
		p.val.SetString(s)
		return nil
	}
	panic("unreachable")
}

func addFlag(val reflect.Value, cmdline *flag.FlagSet, names []string, env, defstr, usage string, ptr unsafe.Pointer) (string, bool) {
	iterNames := func(fn func(name string)) {
		for _, name := range names {
			fn(name)
		}
	}

	var fval flag.Value
	switch val.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		fval = &commonflagValue{val}
	default:
		if !reflect.PtrTo(val.Type()).Implements(flagValueType) {
			return "", false
		}
		fval = val.Addr().Interface().(flag.Value)
	}
	var valApplied bool
	if env != "" {
		enval := os.Getenv(env)
		if enval != "" {
			valApplied = fval.Set(enval) == nil
		}
	}

	if defstr != "" {
		if !valApplied {
			valApplied = fval.Set(defstr) == nil
		}
	}
	if defstr != "" && val.Kind() == reflect.String {
		defstr = strconv.Quote(defstr)
	}
	iterNames(func(name string) {
		cmdline.Var(fval, name, usage)
	})

	return defstr, true
}

type Parser struct {
	Usage UsageFunc

	CommandResolver CommandResolveFunc
}

func (p *Parser) resolveSubCommand(commands []Command, args []string) (Command, []string, error) {
	cmdname := args[0]
	lookup := func(name string) (Command, bool) {
		for _, cmd := range commands {
			if cmd.Name == name {
				return cmd, true
			}
		}
		return Command{}, false
	}
	cmd, ok := lookup(cmdname)
	if ok {
		return cmd, args, nil
	}

	if p.CommandResolver != nil {
		if args, ok := p.CommandResolver(args, commands); ok {
			cmd, ok := lookup(args[0])
			if ok {
				return cmd, args, nil
			}
		}
	}

	return Command{}, nil, fmt.Errorf("unknown command: %s", cmdname)
}

func (p *Parser) parse(args []string, flagsPtr interface{}, commands []Command) (subcmd Command, subcommand []string, err error) {
	flags := commandFlags{
		name:        args[0],
		subcommands: commands,
		usage:       p.Usage,
	}
	cmdline := flag.NewFlagSet(args[0], flag.ContinueOnError)
	cmdline.Usage = flags.printHelp
	if flagsPtr == nil {
		// check for help flag
		err := cmdline.Parse(args[1:])
		if err != nil {
			return subcmd, nil, err
		}
		nonFlagArgs := cmdline.Args()
		if len(commands) > 0 {
			if len(nonFlagArgs) == 0 {
				return subcmd, nil, fmt.Errorf("no command to be run")
			}
			return p.resolveSubCommand(commands, nonFlagArgs)
		}
		if len(nonFlagArgs) > 0 {
			return subcmd, nil, errors.New("the command should be runs without arguments")
		}
		return subcmd, nil, nil
	}

	refv := reflect.ValueOf(flagsPtr)
	if refv.Kind() != reflect.Ptr {
		panic("expect pointer of struct")
	}
	refv = refv.Elem()
	if refv.Kind() != reflect.Struct {
		panic("expect pointer of struct")
	}
	numField := refv.NumField()
	reft := refv.Type()

	var (
		nonFlagStringFields []reflect.Value
		nonFlagSliceField   reflect.Value
	)

	for i := 0; i < numField; i++ {
		fval := refv.Field(i)
		ftyp := reft.Field(i)
		ptr := unsafe.Pointer(fval.UnsafeAddr())
		if ftyp.Anonymous {
			continue
		}

		name := ftyp.Tag.Get("name")
		usage := ftyp.Tag.Get("usage")
		env := ftyp.Tag.Get("env")
		if name == "-" {
			continue
		}
		if strings.HasPrefix(name, "#") {
			name := strings.TrimPrefix(name, "#")
			if name == "" {
				name = strings.ToUpper(ftyp.Name)
			}
			switch {
			case ftyp.Type.Kind() == reflect.String:
				nonFlagStringFields = append(nonFlagStringFields, fval)
				flags.stringNonFlags = append(flags.stringNonFlags, flagInfo{
					Name:    name,
					Usage:   usage,
					Type:    "string",
					NonFlag: true,
				})
			case ftyp.Type == reflect.TypeOf((*[]string)(nil)).Elem():
				if nonFlagSliceField.IsValid() {
					panic(fmt.Errorf("duplicated non-flag field of type []string: %s", ftyp.Name))
				}
				nonFlagSliceField = fval
				flags.sliceNonFlag = append(flags.sliceNonFlag, flagInfo{
					Name:         name,
					Usage:        usage,
					Type:         "string",
					NonFlagSlice: true,
				})
			default:
				panic(fmt.Errorf("only string/[]string allowed for non-flag field: %s", ftyp.Name))
			}

			continue
		}

		if name == "" {
			if ftyp.Name == "" || !isExported(ftyp.Name) {
				continue
			}
			v, asShort := ftyp.Tag.Lookup("short")
			if asShort {
				if v != "" {
					asShort, _ = strconv.ParseBool(v)
				}
			}
			if asShort {
				name = strings.ToLower(ftyp.Name[:1])
			} else {
				name = strings.ToLower(ftyp.Name[:1]) + ftyp.Name[1:]
			}
		}

		names := splitAndTrim(name)
		defstr := ftyp.Tag.Get("default")
		defstr, ok := addFlag(fval, cmdline, names, env, defstr, usage, ptr)
		if !ok {
			continue
		}

		for i := range names {
			names[i] = "-" + names[i]
		}
		flags.flags = append(flags.flags, flagInfo{
			Name:    strings.Join(names, "/"),
			Usage:   usage,
			Type:    ftyp.Type.Kind().String(),
			Env:     env,
			Default: defstr,
			NonFlag: true,
		})
	}
	if nonFlagSliceField.IsValid() && len(commands) > 0 {
		panic(fmt.Errorf("non-flag field of type []string is not allowed with sub commands: %s", flags.sliceNonFlag[0].Name))
	}

	err = cmdline.Parse(args[1:])
	if err != nil {
		return subcmd, nil, err
	}

	nonflagArgs := cmdline.Args()
	var consumedNonFlagArgs int
	for i, s := range nonflagArgs {
		if i < len(nonFlagStringFields) {
			nonFlagStringFields[i].SetString(s)
			consumedNonFlagArgs = i + 1
		} else if nonFlagSliceField.IsValid() {
			nonFlagSliceField.Set(reflect.ValueOf(nonflagArgs[i:]))
			consumedNonFlagArgs = len(nonflagArgs)
			break
		} else {
			break
		}
	}
	if consumedNonFlagArgs < len(nonflagArgs) {
		if len(commands) == 0 {
			if len(nonFlagStringFields) == 0 {
				return subcmd, nil, fmt.Errorf("non-flag args not allowed: %v", nonflagArgs)
			}
			return subcmd, nil, fmt.Errorf("accept only %d non-flag args: %v", len(nonFlagStringFields), nonflagArgs)
		}
		return p.resolveSubCommand(commands, nonflagArgs[consumedNonFlagArgs:])
	}
	if len(commands) > 0 {
		return subcmd, nil, fmt.Errorf("no command to be run")
	}
	return subcmd, nil, nil
}

func (p *Parser) Parse(args []string, ptr interface{}) error {
	_, _, err := p.parse(args, ptr, nil)
	return err
}

func (p *Parser) MustParse(args []string, flags interface{}) {
	err := p.Parse(args, flags)
	handleError(err)
}

func (p *Parser) ParseCommand(args []string, globalFlags interface{}, commands ...Command) (cmd Command, cmdArgs []string, err error) {
	if len(commands) == 0 {
		panic("should provide at least one command.")
	}
	return p.parse(args, globalFlags, commands)
}

func (p *Parser) MustParseCommand(args []string, globalFlags interface{}, commands ...Command) (cmd Command, cmdArgs []string) {
	cmd, cmdArgs, err := p.ParseCommand(args, globalFlags, commands...)
	handleError(err)
	return cmd, cmdArgs
}

func (p *Parser) RunCommand(args []string, globalFlags interface{}, commands ...Command) {
	cmd, cmdArgs, err := p.ParseCommand(args, globalFlags, commands...)
	if err != nil {
		handleError(err)
		return
	}

	if globalFlags == nil {
		if cmd.Run != nil {
			cmd.Run(cmdArgs)
		} else {
			panic(fmt.Errorf("Command.Run is nil: %s", cmd.Name))
		}
	} else {
		if cmd.Run != nil {
			cmd.Run(cmdArgs)
		} else if cmd.RunWithFlags != nil {
			cmd.RunWithFlags(globalFlags, cmdArgs)
		} else {
			panic(fmt.Errorf("Command.Run/RunWithFlags are both nil: %s", cmd.Name))
		}
	}
}

func Parse(args []string, ptr interface{}) error {
	return (&Parser{}).Parse(args, ptr)
}
func MustParse(args []string, ptr interface{}) {
	(&Parser{}).MustParse(args, ptr)
}
func ParseCommand(args []string, globalFlagsPtr interface{}, commands ...Command) (cmd Command, cmdArgs []string, err error) {
	return (&Parser{}).ParseCommand(args, globalFlagsPtr, commands...)
}
func MustParseCommand(args []string, globalFlagsPtr interface{}, commands ...Command) (cmd Command, cmdArgs []string) {
	return (&Parser{}).MustParseCommand(args, globalFlagsPtr, commands...)
}
func RunCommand(args []string, globalFlagsPtr interface{}, commands ...Command) {
	(&Parser{}).RunCommand(args, globalFlagsPtr, commands...)
}
func handleError(err error) {
	if err != nil {
		if errors.Is(err, ErrHelp) {
			os.Exit(0)
		} else {
			os.Exit(1)
		}
	}
}
func tabWriter(out io.Writer, width int) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, width, ' ', 0)
}

func isExported(name string) bool {
	ch, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(ch)
}

func splitAndTrim(name string) []string {
	names := strings.Split(name, ",")
	var end int
	for i := range names {
		names[i] = strings.TrimPrefix(strings.TrimSpace(names[i]), "-")
		if names[i] != "" {
			names[end] = names[i]
			end++
		}
	}
	return names[:end]
}

func fprintf(w io.Writer, format string, v ...interface{}) {
	_, _ = fmt.Fprintf(w, format, v...)
}
func fprintln(w io.Writer, v ...interface{}) {
	_, _ = fmt.Fprintln(w, v...)
}
