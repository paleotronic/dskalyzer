package main

import (
	"fmt"
	"io/ioutil"
	"runtime/debug"
	"strings"
	"time"

	"path/filepath"

	"sort"

	"os"

	"regexp"

	"strconv"

	"errors"

	"github.com/chzyer/readline"
	"github.com/paleotronic/dskalyzer/disk"
	"github.com/paleotronic/dskalyzer/loggy"
	"github.com/paleotronic/dskalyzer/panic"
)

const MAXVOL = 8

var commandList map[string]*shellCommand
var commandVolumes [MAXVOL]*disk.DSKWrapper
var commandTarget int = -1
var commandPath string

func mountDsk(dsk *disk.DSKWrapper) (int, error) {

	var fr []int

	for i, d := range commandVolumes {
		if d == nil {
			fr = append(fr, i)
		} else if dsk.Filename == d.Filename {
			return i, nil
		}
	}

	if len(fr) == 0 {
		return -1, errors.New("No free slots")
	}

	commandVolumes[fr[0]] = dsk

	return fr[0], nil

}

func smartSplit(line string) (string, []string) {

	var out []string

	var inqq bool
	var lastEscape bool
	var chunk string

	add := func() {
		if chunk != "" {
			out = append(out, chunk)
			chunk = ""
		}
	}

	for _, ch := range line {
		switch {
		case ch == '"':
			inqq = !inqq
			add()
		case ch == ' ':
			if inqq || lastEscape {
				chunk += string(ch)
			} else {
				add()
			}
			lastEscape = false
		case ch == '\\':
			lastEscape = true
		default:
			chunk += string(ch)
		}
	}

	add()

	if len(out) == 0 {
		return "", out
	}

	return out[0], out[1:]
}

func getPrompt(wp string, t int) string {

	if t == -1 || commandVolumes[t] == nil {
		return fmt.Sprintf("dsk:%d:%s:%s> ", 0, "<no mount>", wp)
	}

	dsk := commandVolumes[t]

	if dsk != nil {
		return fmt.Sprintf("dsk:%d:%s:%s> ", t, filepath.Base(dsk.Filename), wp)
	}
	return "dsk> "
}

type shellCommand struct {
	Name             string
	Description      string
	MinArgs, MaxArgs int
	Code             func(args []string) int
	NeedsMount       bool
	Context          shellCommandContext
	Text             []string
}

type shellCommandContext int

const (
	sccNone shellCommandContext = 1 << iota
	sccLocal
	sccDiskFile
	sccCommand
	sccAnyFile = sccDiskFile | sccLocal
	sccAny     = sccAnyFile | sccCommand
)

type shellCompleter struct {
}

func hasPrefix(str []rune, prefix []rune) bool {
	if len(prefix) > len(str) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if str[i] != prefix[i] {
			return false
		}
	}
	return true
}

func (sc *shellCompleter) Do(line []rune, pos int) ([][]rune, int) {

	prefix := ""
	chunk := ""
	for _, ch := range line {
		if ch == ' ' {
			prefix = chunk
			break
		} else {
			chunk += string(ch)
		}
	}

	chunk = ""
	cprefix := ""
	var lastEscape bool
	for i := 0; i < pos; i++ {
		ch := line[i]
		switch {
		case ch == '\\':
			lastEscape = true
		case ch == ' ' && !lastEscape:
			cprefix = chunk
			chunk = ""
			lastEscape = false
		default:
			chunk += string(ch)
		}
	}
	if chunk != "" {
		cprefix = chunk
	}

	var context shellCommandContext = sccNone
	cmd, match := commandList[prefix]
	if match {
		context = cmd.Context
	} else {
		context = sccCommand
	}

	var items [][]rune
	switch context {
	case sccCommand:
		for k, _ := range commandList {
			items = append(items, []rune(k))
		}
	case sccDiskFile:
		if commandTarget == -1 || commandVolumes[commandTarget] == nil {
			return [][]rune(nil), 0
		}
		fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)
		info, err := analyze(0, fullpath)
		if err != nil {
			return [][]rune(nil), 0
		}
		for _, f := range info.Files {
			items = append(items, []rune(f.Filename))
		}
	case sccLocal:
		files, err := filepath.Glob(cprefix + "*")
		//fmt.Println(err)
		if err != nil {
			return items, 0
		}
		for _, v := range files {
			items = append(items, []rune(v))
			//fmt.Println("thing:", v)
		}
	}

	if len(items) == 0 {
		return [][]rune(nil), 0
	}

	//fmt.Printf("Context = %d, CPrefix=%s, Items=%v\n", context, cprefix, items)

	var filt [][]rune
	for _, v := range items {
		if hasPrefix(v, []rune(cprefix)) {
			filt = append(filt, shellEscape(v[len(cprefix):]))
		}
	}
	return filt, len(cprefix)
}

func shellEscape(str []rune) []rune {
	out := make([]rune, 0)
	for _, v := range str {
		if v == ' ' {
			out = append(out, '\\')
		}
		out = append(out, v)
	}
	return out
}

func init() {
	commandList = map[string]*shellCommand{
		"mount": &shellCommand{
			Name:        "mount",
			Description: "Mount a disk image",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellMount,
			NeedsMount:  false,
			Context:     sccLocal,
			Text: []string{
				"mount <diskfile>",
				"",
				"Mounts disk and switches to the new slot",
			},
		},
		"unmount": &shellCommand{
			Name:        "unmount",
			Description: "unmount disk image",
			MinArgs:     0,
			MaxArgs:     1,
			Code:        shellUnmount,
			NeedsMount:  true,
			Context:     sccLocal,
			Text: []string{
				"unmount <slot>",
				"",
				"Unmount the disk in the specified slot (or current slot)",
			},
		},
		"extract": &shellCommand{
			Name:        "extract",
			Description: "extract file from disk image",
			MinArgs:     1,
			MaxArgs:     -1,
			Code:        shellExtract,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"extract <filename|pattern>",
				"",
				"Extracts files from current disk",
			},
		},
		"help": &shellCommand{
			Name:        "help",
			Description: "Shows this help",
			MinArgs:     0,
			MaxArgs:     1,
			Code:        shellHelp,
			NeedsMount:  false,
			Context:     sccCommand,
			Text: []string{
				"help <command>",
				"",
				"Display specific help for command or list of commands",
			},
		},
		"info": &shellCommand{
			Name:        "info",
			Description: "Information about the current disk",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellInfo,
			NeedsMount:  true,
			Context:     sccNone,
			Text: []string{
				"info",
				"",
				"Display information on current disk",
			},
		},
		"analyze": &shellCommand{
			Name:        "analyze",
			Description: "Process disk using dskalyzer analytics",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellAnalyze,
			NeedsMount:  true,
			Context:     sccNone,
			Text: []string{
				"analyze",
				"",
				"Display detailed dskalyzer information on current disk",
			},
		},
		"quit": &shellCommand{
			Name:        "quit",
			Description: "Leave this place",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellQuit,
			NeedsMount:  false,
			Context:     sccNone,
		},
		"cat": &shellCommand{
			Name:        "cat",
			Description: "Display file information",
			MinArgs:     0,
			MaxArgs:     1,
			Code:        shellCat,
			NeedsMount:  true,
			Context:     sccNone,
			Text: []string{
				"cat [<pattern>]",
				"",
				"List files on current disk (can use wildcards).",
			},
		},
		"mkdir": &shellCommand{
			Name:        "mkdir",
			Description: "Create a directory on disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellMkdir,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"mkdir <directory>",
				"",
				"Create directory on current disk (if supported)",
			},
		},
		"put": &shellCommand{
			Name:        "put",
			Description: "Copy local file to disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellPut,
			NeedsMount:  true,
			Context:     sccLocal,
			Text: []string{
				"put <local file>",
				"",
				"Write local file to current disk",
			},
		},
		"delete": &shellCommand{
			Name:        "delete",
			Description: "Remove file from disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellDelete,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"delete <filename>",
				"",
				"Delete file from current disk",
			},
		},
		"ingest": &shellCommand{
			Name:        "ingest",
			Description: "Ingest directory containing disks (or single disk) into system",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellIngest,
			NeedsMount:  false,
			Context:     sccLocal,
			Text: []string{
				"ingest <disk name>",
				"",
				"Catalog diskfile into dskalyzer database.",
			},
		},
		"lock": &shellCommand{
			Name:        "lock",
			Description: "Lock file on the disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellLock,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"lock <diskfile>",
				"",
				"Make file on disk read-only",
			},
		},
		"unlock": &shellCommand{
			Name:        "unlock",
			Description: "Unlock file on the disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellUnlock,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"unlock <diskfile>",
				"",
				"Make file on disk writable",
			},
		},
		"ls": &shellCommand{
			Name:        "ls",
			Description: "List local files",
			MinArgs:     0,
			MaxArgs:     999,
			Code:        shellListFiles,
			NeedsMount:  false,
			Context:     sccLocal,
			Text: []string{
				"ls <pattern>",
				"",
				"List local files",
			},
		},
		"cd": &shellCommand{
			Name:        "cd",
			Description: "Change local path",
			MinArgs:     0,
			MaxArgs:     1,
			Code:        shellCd,
			NeedsMount:  false,
			Context:     sccLocal,
			Text: []string{
				"cd <path>",
				"",
				"Change local directory",
			},
		},
		"disks": &shellCommand{
			Name:        "disks",
			Description: "List mounted volumes",
			MinArgs:     0,
			MaxArgs:     0,
			Code:        shellDisks,
			NeedsMount:  false,
			Context:     sccNone,
			Text: []string{
				"disks",
				"",
				"List all mounted volumes",
			},
		},
		"target": &shellCommand{
			Name:        "target",
			Description: "Select mounted volume as default",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellPrefix,
			NeedsMount:  false,
			Context:     sccNone,
			Text: []string{
				"target <slot>",
				"",
				"Select slot as default for commands",
			},
		},
		"copy": &shellCommand{
			Name:        "copy",
			Description: "Copy files from one volume to another",
			MinArgs:     2,
			MaxArgs:     999,
			Code:        shellD2DCopy,
			NeedsMount:  false,
			Context:     sccDiskFile,
			Text: []string{
				"copy [<slot>:]<pattern> <slot>:[<path>]",
				"",
				"Copy files from one mounted disk to another.",
				"Example:",
				"copy 0:*.system 1:",
			},
		},
		"move": &shellCommand{
			Name:        "move",
			Description: "Move files from one volume to another",
			MinArgs:     2,
			MaxArgs:     999,
			Code:        shellD2DCopy,
			NeedsMount:  false,
			Context:     sccDiskFile,
			Text: []string{
				"move [<slot>:]<pattern> <slot>:[<path>]",
				"",
				"Move files from one mounted disk to another.",
				"Example:",
				"move 0:*.system 1:",
			},
		},
		"rename": &shellCommand{
			Name:        "rename",
			Description: "Rename a file on the disk",
			MinArgs:     2,
			MaxArgs:     2,
			Code:        shellRename,
			NeedsMount:  true,
			Context:     sccDiskFile,
			Text: []string{
				"rename <filename> <new filename>",
				"",
				"Rename a file on a disk.",
			},
		},
	}
}

func shellProcess(line string) int {
	line = strings.TrimSpace(line)

	verb, args := smartSplit(line)

	if verb != "" {
		verb = strings.ToLower(verb)
		command, ok := commandList[verb]
		if ok {
			fmt.Println()
			var cok = true
			if command.MinArgs != -1 {
				if len(args) < command.MinArgs {
					os.Stderr.WriteString(fmt.Sprintf("%s expects at least %d arguments\n", verb, command.MinArgs))
					cok = false
				}
			}
			if command.MaxArgs != -1 {
				if len(args) > command.MaxArgs {
					os.Stderr.WriteString(fmt.Sprintf("%s expects at most %d arguments\n", verb, command.MaxArgs))
					cok = false
				}
			}
			if command.NeedsMount {
				if commandTarget == -1 || commandVolumes[commandTarget] == nil {
					os.Stderr.WriteString(fmt.Sprintf("%s only works on mounted disks\n", verb))
					cok = false
				}
			}
			if cok {
				r := command.Code(args)
				fmt.Println()
				return r
			} else {
				return -1
			}
		} else {
			os.Stderr.WriteString(fmt.Sprintf("Unrecognized command: %s\n", verb))
			return -1
		}
	}

	return 0
}

func shellDo(dsk *disk.DSKWrapper) {

	//commandVolumes = dsk
	commandPath := ""

	ac := &shellCompleter{}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 getPrompt(commandPath, commandTarget),
		HistoryFile:            binpath() + "/.shell_history",
		DisableAutoSaveHistory: false,
		AutoComplete:           ac,
	})
	if err != nil {
		os.Exit(2)
	}
	defer rl.Close()

	running := true

	for running {
		line, err := rl.Readline()
		if err != nil {
			break
		}

		r := shellProcess(line)
		if r == 999 {
			return
		}

		rl.SetPrompt(getPrompt(commandPath, commandTarget))
	}

}

func shellMount(args []string) int {
	if len(args) != 1 {
		fmt.Println("mount expects a diskfile")
		return -1
	}

	dsk, err := disk.NewDSKWrapper(defNibbler, args[0])
	if err != nil {
		os.Stderr.WriteString("Error:" + err.Error() + "\n")
		return -1
	}

	slotid, err := mountDsk(dsk)
	if err != nil {
		os.Stderr.WriteString("Error:" + err.Error() + "\n")
		return -1
	}

	commandTarget = slotid
	os.Stderr.WriteString(fmt.Sprintf("mount disk in slot %d\n", slotid))

	return 0
}

func shellUnmount(args []string) int {

	if len(args) > 0 {
		if shellPrefix(args) == -1 {
			return -1
		}
	}

	if commandVolumes[commandTarget] != nil {

		commandVolumes[commandTarget] = nil
		commandPath = ""

		os.Stderr.WriteString("Unmounted volume\n")

	}

	return 0
}

func shellHelp(args []string) int {

	if len(args) == 0 {
		keys := make([]string, 0)
		for k, _ := range commandList {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			info := commandList[k]
			fmt.Printf("%-10s %s\n", info.Name, info.Description)
		}
	} else {
		command := strings.ToLower(args[0])
		if details, ok := commandList[command]; ok {
			if details.Text != nil {
				for _, l := range details.Text {
					fmt.Println(l)
				}
			} else {
				os.Stderr.WriteString("No help available for " + command)
			}
		} else {
			os.Stderr.WriteString("No help available for " + command)
		}
	}

	return 0
}

func shellInfo(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	fmt.Printf("Disk path   : %s\n", fullpath)
	fmt.Printf("Disk type   : %s\n", commandVolumes[commandTarget].Format.String())
	fmt.Printf("Sector Order: %s\n", commandVolumes[commandTarget].Layout.String())
	fmt.Printf("Size        : %d bytes\n", len(commandVolumes[commandTarget].Data))

	return 0
}

func shellQuit(args []string) int {

	return 999

}

func shellCat(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	info, err := analyze(0, fullpath)
	if err != nil {
		return -1
	}

	bs := 256
	if info.FormatID.ID == disk.DF_PASCAL || info.FormatID.ID == disk.DF_PRODOS ||
		info.FormatID.ID == disk.DF_PRODOS_800KB || info.FormatID.ID == disk.DF_PRODOS_400KB ||
		info.FormatID.ID == disk.DF_PRODOS_CUSTOM {
		bs = 512
	}

	pattern := "*"
	if len(args) > 0 {
		pattern = args[0]
	}

	files, _ := globDisk(commandTarget, pattern)

	fmt.Printf("%-33s  %6s  %2s  %-23s  %s\n", "NAME", "BLOCKS", "RO", "KIND", "ADDITONAL")
	for _, f := range files {
		add := ""
		locked := " "
		if f.LoadAddress != 0 {
			add = fmt.Sprintf("(A$%.4X)", f.LoadAddress)
		}
		if f.Locked {
			locked = "Y"
		}
		fmt.Printf("%-33s  %6d  %2s  %-23s  %s\n", f.Filename, (f.Size/bs)+1, locked, f.Type, add)
	}

	free := 0
	used := 0
	for _, v := range info.Bitmap {
		if v {
			used++
		} else {
			free++
		}
	}

	fmt.Printf("\nUSED: %-20d FREE: %-20d\n", used, free)

	return 0

}

func shellCd(args []string) int {

	if len(args) > 0 {
		err := os.Chdir(args[0])
		if err != nil {
			os.Stderr.WriteString("Change directory failed: " + err.Error() + "\n")
			return -1
		}
	}

	wd, _ := os.Getwd()
	os.Stderr.WriteString("Working directory is now " + wd + "\n")
	return 0

}

func shellListFiles(args []string) int {

	bs := 256

	if len(args) == 0 {
		wd, _ := os.Getwd()
		args = append(args, wd+"/*.*")
	}

	for _, a := range args {

		files, err := filepath.Glob(a)
		if err != nil {
			os.Stderr.WriteString("Error reading path " + a + ": " + err.Error() + "\n")
			continue
		}

		fmt.Printf("%6s  %2s  %-23s  %s\n", "BLOCKS", "RO", "KIND", "NAME")
		for _, f := range files {
			locked := " "
			fi, _ := os.Stat(f)
			if fi.Mode().Perm()&0100 != 0100 {
				locked = "Y"
			}
			fmt.Printf("%6d  %2s  %-23s  %s\n", (int(fi.Size())/bs)+1, locked, "Local file", fi.Name())
		}
	}

	return 0
}

func shellAnalyze(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	info, err := analyze(0, fullpath)
	if err != nil {
		return -1
	}

	fmt.Printf("Format: %s\n", info.FormatID)
	fmt.Printf("Tracks: %d, Sectors: %d\n", info.Tracks, info.Sectors)

	return 0
}

func shellExtract(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	fmt.Println("Extract:", args[0])

	files, _ := globDisk(commandTarget, args[0])

	for _, f := range files {

		err := ExtractFile(fullpath, f, true, true)
		if err == nil {
			fmt.Println("OK")
		} else {
			fmt.Println("FAILED")
			return -1
		}

	}

	return 0

}

func formatIn(f disk.DiskFormatID, list []disk.DiskFormatID) bool {
	for _, v := range list {
		if v == f {
			return true
		}
	}
	return false
}

func fts() string {
	t := time.Now()
	return fmt.Sprintf(
		"%.4d%.2d%.2d%.2d%.2d%.2d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(),
	)
}

func backupFile(path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	path = strings.Replace(path, ":", "", -1)
	path = strings.Replace(path, "\\", "/", -1)

	bpath := binpath() + "/backup/" + path + "." + fts()
	os.MkdirAll(filepath.Dir(bpath), 0755)

	f, err := os.Create(bpath)
	if err != nil {
		return err
	}
	f.Write(data)
	f.Close()

	os.Stderr.WriteString("Backed up disk to: " + bpath + "\n")

	return nil
}

func saveDisk(dsk *disk.DSKWrapper, path string) error {

	backupFile(path)

	f, e := os.Create(path)
	if e != nil {
		return e
	}
	defer f.Close()
	f.Write(dsk.Data)

	fmt.Println("Updated disk " + path)
	return nil
}

func shellMkdir(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	path := ""
	name := args[0]
	if strings.Contains(name, "/") {
		path = filepath.Dir(name)
		name = filepath.Base(name)
	}

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		e := commandVolumes[commandTarget].PRODOSCreateDirectory(path, name)
		if e != nil {
			fmt.Println(e)
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)
	} else {
		fmt.Println("Do not support Mkdir on " + commandVolumes[commandTarget].Format.String() + " currently.")
		return 0
	}

	return 0

}

func isASCII(in []byte) bool {
	for _, v := range in {
		if v > 128 {
			return false
		}
	}
	return true
}

func shellPut(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	data, err := ioutil.ReadFile(args[0])
	if err != nil {
		return -1
	}

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		addr := int64(0x0801)
		name := filepath.Base(args[0])
		kind := disk.FileTypeAPP
		reSpecial := regexp.MustCompile("(?i)^(.+)[#](0x[a-fA-F0-9]+)[.]([A-Za-z]+)$")
		ext := strings.Trim(filepath.Ext(name), ".")
		if reSpecial.MatchString(name) {
			m := reSpecial.FindAllStringSubmatch(name, -1)
			name = m[0][1]
			ext = strings.ToLower(m[0][3])
			addrStr := m[0][2]
			addr, _ = strconv.ParseInt(addrStr, 0, 32)
		} else {
			name = strings.Replace(name, "."+ext, "", -1)
		}

		kind = disk.AppleDOSFileTypeFromExt(ext)

		if strings.HasSuffix(args[0], ".INT.ASC") {
			kind = disk.FileTypeINT
		} else if strings.HasSuffix(args[0], ".APP.ASC") {
			kind = disk.FileTypeAPP
		}

		if kind == disk.FileTypeAPP && isASCII(data) {
			lines := strings.Split(string(data), "\n")
			data = disk.ApplesoftTokenize(lines)
		} else if kind == disk.FileTypeINT && isASCII(data) {
			lines := strings.Split(string(data), "\n")
			data = disk.IntegerTokenize(lines)
			os.Stderr.WriteString("WARNING: Integer retokenization from text is experimental\n")
		}

		e := commandVolumes[commandTarget].AppleDOSWriteFile(name, kind, data, int(addr))
		if e != nil {
			os.Stderr.WriteString("Failed to create file: " + e.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)

	} else if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		addr := int64(0x0801)
		name := filepath.Base(args[0])
		ext := strings.Trim(filepath.Ext(name), ".")
		reSpecial := regexp.MustCompile("(?i)^(.+)[#](0x[a-fA-F0-9]+)[.]([A-Za-z]+)$")
		if reSpecial.MatchString(name) {
			m := reSpecial.FindAllStringSubmatch(name, -1)
			name = m[0][1]
			ext = strings.ToLower(m[0][3])
			addrStr := m[0][2]
			addr, _ = strconv.ParseInt(addrStr, 0, 32)
		} else {
			name = strings.Replace(name, "."+ext, "", -1)
		}

		kind := disk.ProDOSFileTypeFromExt(ext)

		if strings.HasSuffix(args[0], ".INT.ASC") {
			kind = disk.FileType_PD_INT
		} else if strings.HasSuffix(args[0], ".APP.ASC") {
			kind = disk.FileType_PD_APP
		}

		if kind == disk.FileType_PD_APP && isASCII(data) {
			lines := strings.Split(string(data), "\n")
			data = disk.ApplesoftTokenize(lines)
		} else if kind == disk.FileType_PD_INT && isASCII(data) {
			lines := strings.Split(string(data), "\n")
			data = disk.IntegerTokenize(lines)
			os.Stderr.WriteString("WARNING: Integer retokenization from text is experimental\n")
		}

		e := commandVolumes[commandTarget].PRODOSWriteFile(commandPath, name, kind, data, int(addr))
		if e != nil {
			os.Stderr.WriteString("Failed to create file: " + e.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)

	} else {
		os.Stderr.WriteString("Writing files not supported on " + commandVolumes[commandTarget].Format.String())
		return -1
	}

	return 0

}

func shellDelete(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandVolumes[commandTarget].AppleDOSDeleteFile(args[0])
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)

	} else if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {

		if strings.Contains(args[0], "/") {
			commandPath = filepath.Dir(args[0])
			args[0] = filepath.Base(args[0])
		}

		err = commandVolumes[commandTarget].PRODOSDeleteFile(commandPath, args[0])
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)
	} else {
		os.Stderr.WriteString("Deleting files not supported on " + commandVolumes[commandTarget].Format.String())
		return -1
	}

	return 0

}

func shellIngest(args []string) int {

	dskName := args[0]

	info, err := os.Stat(dskName)
	if err != nil {
		loggy.Get(0).Errorf("Error stating file: %s", err.Error())
		os.Exit(2)
	}
	if info.IsDir() {
		walk(dskName)
	} else {
		indisk = make(map[disk.DiskFormat]int)
		outdisk = make(map[disk.DiskFormat]int)

		panic.Do(
			func() {
				var e error
				_, e = analyze(0, dskName)
				// handle any disk specific
				if e != nil {
					os.Stderr.WriteString("Error processing disk")
				}
			},
			func(r interface{}) {
				loggy.Get(0).Errorf("Error processing volume: %s", dskName)
				loggy.Get(0).Errorf(string(debug.Stack()))
			},
		)
	}

	return 0
}

func shellLock(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandVolumes[commandTarget].AppleDOSSetLocked(args[0], true)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)

	} else if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {

		if strings.Contains(args[0], "/") {
			commandPath = filepath.Dir(args[0])
			args[0] = filepath.Base(args[0])
		}

		err = commandVolumes[commandTarget].PRODOSSetLocked(commandPath, args[0], true)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)
	} else {
		os.Stderr.WriteString("Locking files not supported on " + commandVolumes[commandTarget].Format.String())
		return -1
	}

	return 0
}

func shellUnlock(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandVolumes[commandTarget].AppleDOSSetLocked(args[0], false)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)

	} else if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {

		if strings.Contains(args[0], "/") {
			commandPath = filepath.Dir(args[0])
			args[0] = filepath.Base(args[0])
		}

		err = commandVolumes[commandTarget].PRODOSSetLocked(commandPath, args[0], false)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandVolumes[commandTarget], fullpath)
	} else {
		os.Stderr.WriteString("Locking files not supported on " + commandVolumes[commandTarget].Format.String())
		return -1
	}

	return 0
}

func shellDisks(args []string) int {

	fmt.Println("Mounted Volumes")
	for i, d := range commandVolumes {
		if d != nil {
			fmt.Printf("%d:%s\n", i, d.Filename)
		}
	}

	return 0
}

func shellPrefix(args []string) int {

	tmp, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil {
		os.Stderr.WriteString("Invalid slot number: " + args[0] + "\n")
		return -1
	}

	slotid := int(tmp)
	if slotid < 0 || slotid >= MAXVOL {
		os.Stderr.WriteString(fmt.Sprintf("Valid slots are %d to %d.\n", 0, MAXVOL-1))
		return -1
	}

	d := commandVolumes[slotid]
	if d == nil {
		os.Stderr.WriteString(fmt.Sprintf("Nothing mounted in slot %d (use disks to see mounts)\n", slotid))
		return -1
	}

	commandTarget = slotid

	return 0

}

func shellD2DCopy(args []string) int {

	reCopyArg := regexp.MustCompile("(?i)^(([0-9])[:])?(.+)$")
	reCopyTarget := regexp.MustCompile("(?i)^(([0-9])[:])?(.+)?$")

	l := len(args)
	sources := args[0 : l-1]
	target := args[l-1]

	var allfiles []*DiskFile

	for _, arg := range sources {
		if reCopyArg.MatchString(arg) {
			m := reCopyArg.FindAllStringSubmatch(arg, -1)
			volume := commandTarget
			if m[0][2] != "" {
				tmp, err := strconv.ParseInt(m[0][2], 10, 32)
				if err != nil {
					os.Stderr.WriteString("Invalid slot number: " + m[0][2] + "\n")
					return -1
				}
				volume = int(tmp)
			}
			patternstr := m[0][3]
			files, _ := globDisk(volume, patternstr)
			allfiles = append(allfiles, files...)
		}
	}

	if reCopyTarget.MatchString(target) {
		m := reCopyTarget.FindAllStringSubmatch(target, -1)
		volume := commandTarget
		if m[0][2] != "" {
			tmp, err := strconv.ParseInt(m[0][2], 10, 32)
			if err != nil {
				os.Stderr.WriteString("Invalid slot number: " + m[0][2] + "\n")
				return -1
			}
			volume = int(tmp)
		}
		path := m[0][3]
		v := commandVolumes[volume]
		if v == nil {
			os.Stderr.WriteString("Invalid slot number: " + m[0][2] + "\n")
			return -1
		}
		if !formatIn(
			v.Format.ID,
			[]disk.DiskFormatID{
				disk.DF_DOS_SECTORS_13,
				disk.DF_DOS_SECTORS_16,
				disk.DF_PRODOS,
				disk.DF_PRODOS_800KB,
				disk.DF_PRODOS_400KB,
				disk.DF_PRODOS_CUSTOM,
			}) {
			os.Stderr.WriteString("Target volume does not support write.\n")
			return -1
		}
		if path != "" && len(allfiles) > 1 {
			// copy to path
			if !formatIn(
				v.Format.ID,
				[]disk.DiskFormatID{
					disk.DF_PRODOS,
					disk.DF_PRODOS_800KB,
					disk.DF_PRODOS_400KB,
					disk.DF_PRODOS_CUSTOM,
				}) {
				os.Stderr.WriteString("Only prodos supports copy to directory")
				return -1
			}
			for _, f := range allfiles {
				// must be prodos
				name := f.Filename
				if len(name) > 15 {
					name = name[:15]
				}
				kind := disk.ProDOSFileTypeFromExt(f.Ext)
				auxtype := f.LoadAddress
				data := f.Data
				e := v.PRODOSWriteFile(path, name, kind, data, auxtype)
				if e != nil {
					os.Stderr.WriteString(fmt.Sprintf("Failed to copy %s: %s\n", name, e.Error()))
					return -1
				}
				os.Stderr.WriteString(fmt.Sprintf("Copied %s (%d bytes)\n", name, len(data)))
			}
		} else {
			for _, f := range allfiles {
				name := f.Filename
				if path != "" && len(allfiles) == 1 {
					name = path
				}

				if formatIn(
					v.Format.ID,
					[]disk.DiskFormatID{
						disk.DF_PRODOS,
						disk.DF_PRODOS_800KB,
						disk.DF_PRODOS_400KB,
						disk.DF_PRODOS_CUSTOM,
					}) {

					if len(name) > 15 {
						name = name[:15]
					}
					kind := disk.ProDOSFileTypeFromExt(f.Ext)
					auxtype := f.LoadAddress
					data := f.Data
					e := v.PRODOSWriteFile("", name, kind, data, auxtype)
					if e != nil {
						os.Stderr.WriteString(fmt.Sprintf("Failed to copy %s: %s\n", name, e.Error()))
						return -1
					}
					os.Stderr.WriteString(fmt.Sprintf("Copied %s (%d bytes)\n", name, len(data)))
				} else {
					// DOS
					kind := disk.AppleDOSFileTypeFromExt(f.Ext)
					auxtype := f.LoadAddress
					data := f.Data
					e := v.AppleDOSWriteFile(name, kind, data, auxtype)
					if e != nil {
						os.Stderr.WriteString(fmt.Sprintf("Failed to copy %s: %s\n", name, e.Error()))
						return -1
					}
					os.Stderr.WriteString(fmt.Sprintf("Copied %s (%d bytes)\n", name, len(data)))
				}
			}
		}

		// here need to publish disk
		fullpath, _ := filepath.Abs(v.Filename)
		saveDisk(v, fullpath)

	} else {
		os.Stderr.WriteString("Invalid target: " + target + "\n")
		return -1
	}

	return 0
}

func shellRename(args []string) int {

	fullpath, _ := filepath.Abs(commandVolumes[commandTarget].Filename)

	if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {

		oldname := filepath.Base(args[0])
		oldpath := filepath.Dir(args[0])
		newname := filepath.Base(args[1])

		if oldpath == "." {
			oldpath = ""
		}

		fmt.Println(oldname, newname, oldpath)

		e := commandVolumes[commandTarget].PRODOSRenameFile(oldpath, oldname, newname)
		if e != nil {
			os.Stderr.WriteString("Unable to rename file: " + e.Error())
			return -1
		}

	} else if formatIn(commandVolumes[commandTarget].Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		oldname := filepath.Base(args[0])
		newname := filepath.Base(args[1])

		e := commandVolumes[commandTarget].AppleDOSRenameFile(oldname, newname)
		if e != nil {
			os.Stderr.WriteString("Unable to rename file: " + e.Error())
			return -1
		}
	} else {
		os.Stderr.WriteString("Rename currently unsupported on " + commandVolumes[commandTarget].Format.String() + "\n")
		return -1
	}

	saveDisk(commandVolumes[commandTarget], fullpath)

	return 0
}

func globDisk(slotid int, pattern string) ([]*DiskFile, error) {

	var files []*DiskFile

	if commandVolumes[slotid] == nil {
		return []*DiskFile(nil), fmt.Errorf("Invalid slotid %d", slotid)
	}

	fullpath, _ := filepath.Abs(commandVolumes[slotid].Filename)

	dsk, err := analyze(0, fullpath)
	if err != nil {
		return []*DiskFile(nil), fmt.Errorf("Problem reading volume")
	}

	r := strings.Replace(pattern, ".", "[.]", -1)
	r = strings.Replace(r, "?", ".", -1)
	r = strings.Replace(r, "*", ".*", -1)
	r = "(?i)^" + r + "$"

	rePattern := regexp.MustCompile(r)

	for _, f := range dsk.Files {
		if rePattern.MatchString(f.Filename) {
			files = append(files, f)
		}
	}

	return files, nil

}
