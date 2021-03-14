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

type UsageFunc func(printDefaults func(w io.Writer))

type Command struct {
	Name  string
	Usage string

	Run func(args []string)
}

type CommandList struct {
	Commands []Command

	Usage UsageFunc
}

func (c CommandList) Run(args []string) {
	var name string
	if len(args) > 1 {
		name = args[1]
	}
	if name == "" || isHelpFlag(name) {
		c.usage()
		return
	}

	for i := range c.Commands {
		if c.Commands[i].Name == name {
			c.Commands[i].Run(args[1:])
			return
		}
	}
	fprintf(os.Stderr, "unsupported command `%s`,\n", name)
	os.Exit(1)
}

func (c CommandList) usage() {
	if c.Usage == nil {
		c.printDefaults(os.Stderr)
	} else {
		c.Usage(c.printDefaults)
	}
}

func (c CommandList) printDefaults(w io.Writer) {
	tw := tabWriter(w, 2)
	fprintln(tw, "Available commands:")
	for _, c := range c.Commands {
		fprintf(tw, "\t%s\t%s\n", c.Name, c.Usage)
	}
	_ = tw.Flush()
}

type flagInfo struct {
	Name    string
	Default interface{}
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
}

func printDefaultValue(value interface{}) (string, bool) {
	if value == nil {
		return "", false
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		b := v.Bool()
		return "true", b
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := v.Int()
		if i == 0 {
			return "", false
		}
		return strconv.Itoa(int(i)), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i := v.Uint()
		if i == 0 {
			return "", false
		}
		return strconv.FormatUint(i, 10), true
	case reflect.String:
		s := v.String()
		return strconv.Quote(s), s != ""
	default:
		return "", false
	}
}

func (c commandFlags) printDefaults(w io.Writer) {
	if len(c.flags)+len(c.stringNonFlags)+len(c.sliceNonFlag) == 0 {
		fprintln(w, "no options.")
		return
	}
	tw := tabWriter(w, 2)

	if len(c.stringNonFlags) > 0 || len(c.sliceNonFlag) > 0 {
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
		fprintf(tw, "\n")
	} else {
		fprintf(tw, "Usage of %s:\n", c.name)
	}
	for _, fs := range [][]flagInfo{c.flags, c.stringNonFlags, c.sliceNonFlag} {
		for _, f := range fs {
			fprintf(tw, "\t%s\t%s", f.Name, f.Type)
			if str, ok := printDefaultValue(f.Default); ok {
				fprintf(tw, ` (default: %s)`, str)
			}
			fprintln(tw)
			if f.Usage != "" {
				fprintf(tw, "\t\t%s\n", f.Usage)
			}
		}
	}
	_ = tw.Flush()
}

var flagValueType = reflect.TypeOf((*flag.Value)(nil)).Elem()

func addFlag(val reflect.Value, cmdline *flag.FlagSet, names []string, defstr, usage string, ptr unsafe.Pointer) (interface{}, bool) {
	iterNames := func(fn func(name string)) {
		for _, name := range names {
			fn(name)
		}
	}

	var (
		defval interface{}
		skip   bool
	)
	switch val.Kind() {
	case reflect.Bool:
		def, _ := strconv.ParseBool(defstr)
		defval = def
		iterNames(func(name string) {
			cmdline.BoolVar((*bool)(ptr), name, def, usage)
		})
	case reflect.Int:
		def, _ := strconv.ParseInt(defstr, 10, strconv.IntSize)
		iterNames(func(name string) {
			cmdline.IntVar((*int)(ptr), name, int(def), usage)
		})
	case reflect.Int64:
		def, _ := strconv.ParseInt(defstr, 10, 64)
		defval = def
		iterNames(func(name string) {
			cmdline.Int64Var((*int64)(ptr), name, def, usage)
		})
	case reflect.Uint:
		def, _ := strconv.ParseUint(defstr, 10, strconv.IntSize)
		defval = def
		iterNames(func(name string) {
			cmdline.UintVar((*uint)(ptr), name, uint(def), usage)
		})
	case reflect.Uint64:
		def, _ := strconv.ParseUint(defstr, 10, 64)
		defval = def
		iterNames(func(name string) {
			cmdline.Uint64Var((*uint64)(ptr), name, def, usage)
		})
	case reflect.Float64:
		def, _ := strconv.ParseFloat(defstr, 64)
		defval = def
		iterNames(func(name string) {
			cmdline.Float64Var((*float64)(ptr), name, def, usage)
		})
	case reflect.String:
		defval = defstr
		iterNames(func(name string) {
			cmdline.StringVar((*string)(ptr), name, defstr, usage)
		})
	default:
		defval = defstr
		if reflect.PtrTo(val.Type()).Implements(flagValueType) {
			val := val.Addr().Interface().(flag.Value)
			if defstr != "" {
				_ = val.Set(defstr)
			}
			iterNames(func(name string) {
				cmdline.Var(val, name, usage)
			})
		} else {
			skip = true
		}
	}

	return defval, !skip
}

func Parse(args []string, usage UsageFunc, ptr interface{}) error {
	var err error
	if ptr == nil {
		if len(args) > 1 {
			err = errors.New("the command should be runs without arguments")
		}
		return nil
	}

	cmdline := flag.NewFlagSet(args[0], flag.ContinueOnError)

	refv := reflect.ValueOf(ptr)
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

		flags = commandFlags{
			name: args[0],
		}
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
		if name == "-" {
			continue
		}
		if name == "#nonflag" {
			switch {
			case ftyp.Type.Kind() == reflect.String:
				nonFlagStringFields = append(nonFlagStringFields, fval)
				flags.stringNonFlags = append(flags.stringNonFlags, flagInfo{
					Name:    strings.ToUpper(ftyp.Name),
					Usage:   usage,
					Type:    "string",
					NonFlag: true,
				})
			case ftyp.Type == reflect.TypeOf((*[]string)(nil)).Elem():
				if nonFlagSliceField.IsValid() {
					panic(fmt.Errorf("duplicated `#nonflag` field of type []string: %s", ftyp.Name))
				}
				nonFlagSliceField = fval
				flags.sliceNonFlag = append(flags.sliceNonFlag, flagInfo{
					Name:         strings.ToUpper(ftyp.Name),
					Usage:        usage,
					Type:         "string",
					NonFlagSlice: true,
				})
			default:
				panic(fmt.Errorf("only string/[]string allowed for `#nonflag` field: %s", ftyp.Name))
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
		defval, ok := addFlag(fval, cmdline, names, defstr, usage, ptr)
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
			Default: defval,
			NonFlag: true,
		})
	}

	cmdline.Usage = func() {
		if usage == nil {
			flags.printDefaults(os.Stderr)
		} else {
			usage(flags.printDefaults)
		}
	}
	err = cmdline.Parse(args[1:])
	if err != nil {
		return err
	}

	nonflagArgs := cmdline.Args()
	for i, s := range nonflagArgs {
		if i < len(nonFlagStringFields) {
			nonFlagStringFields[i].SetString(s)
		} else if nonFlagSliceField.IsValid() {
			nonFlagSliceField.Set(reflect.ValueOf(nonflagArgs[i:]))
			break
		} else {
			if len(nonFlagStringFields) == 0 {
				return fmt.Errorf("non-flag args not allowed: %v", nonflagArgs)
			}
			return fmt.Errorf("accept only %d non-flag args: %v", len(nonFlagStringFields), nonflagArgs)
		}
	}
	return nil
}

func MustParse(args []string, usage UsageFunc, flags interface{}) {
	err := Parse(args, usage, flags)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		} else {
			fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}
}

func tabWriter(out io.Writer, width int) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, width, ' ', 0)
}

func isHelpFlag(name string) bool {
	return name == "-h" || name == "-help" || name == "--help"
}

func isExported(name string) bool {
	ch, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(ch)
}

func splitAndTrim(name string) []string {
	names := strings.Split(name, ",")
	var end int
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
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
