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

	"github.com/chzyer/readline"
	"github.com/paleotronic/dskalyzer/disk"
	"github.com/paleotronic/dskalyzer/loggy"
	"github.com/paleotronic/dskalyzer/panic"
)

func smartSplit(line string) (string, []string) {

	var out []string

	var inqq bool
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
		case ch == ' ' || ch == '\t':
			if inqq {
				chunk += string(ch)
			} else {
				add()
			}
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

func getPrompt(wp string, dsk *disk.DSKWrapper) string {
	if dsk != nil {
		return fmt.Sprintf("dsk:%s:%s> ", filepath.Base(dsk.Filename), wp)
	}
	return "dsk> "
}

type shellCommand struct {
	Name             string
	Description      string
	MinArgs, MaxArgs int
	Code             func(args []string) int
	NeedsMount       bool
}

var commandList map[string]*shellCommand
var commandTarget *disk.DSKWrapper
var commandPath string

func init() {
	commandList = map[string]*shellCommand{
		"mount": &shellCommand{
			Name:        "mount",
			Description: "Mount a disk image",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellMount,
			NeedsMount:  false,
		},
		"unmount": &shellCommand{
			Name:        "unmount",
			Description: "unmount disk image",
			MinArgs:     0,
			MaxArgs:     0,
			Code:        shellUnmount,
			NeedsMount:  true,
		},
		"extract": &shellCommand{
			Name:        "extract",
			Description: "extract file from disk image",
			MinArgs:     1,
			MaxArgs:     -1,
			Code:        shellExtract,
			NeedsMount:  true,
		},
		"help": &shellCommand{
			Name:        "help",
			Description: "Shows this help",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellHelp,
			NeedsMount:  false,
		},
		"info": &shellCommand{
			Name:        "info",
			Description: "Information about the current disk",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellInfo,
			NeedsMount:  true,
		},
		"analyze": &shellCommand{
			Name:        "analyze",
			Description: "Process disk using dskalyzer analytics",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellAnalyze,
			NeedsMount:  true,
		},
		"quit": &shellCommand{
			Name:        "quit",
			Description: "Leave this place",
			MinArgs:     -1,
			MaxArgs:     -1,
			Code:        shellQuit,
			NeedsMount:  false,
		},
		"cat": &shellCommand{
			Name:        "cat",
			Description: "Display file information",
			MinArgs:     0,
			MaxArgs:     1,
			Code:        shellCat,
			NeedsMount:  true,
		},
		"mkdir": &shellCommand{
			Name:        "mkdir",
			Description: "Create a directory",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellMkdir,
			NeedsMount:  true,
		},
		"put": &shellCommand{
			Name:        "put",
			Description: "Copy local file to disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellPut,
			NeedsMount:  true,
		},
		"delete": &shellCommand{
			Name:        "delete",
			Description: "Remove file from disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellDelete,
			NeedsMount:  true,
		},
		"ingest": &shellCommand{
			Name:        "ingest",
			Description: "Ingest directory containing disks (or single disk) into system",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellIngest,
			NeedsMount:  false,
		},
		"lock": &shellCommand{
			Name:        "lock",
			Description: "Lock file on the disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellLock,
			NeedsMount:  true,
		},
		"unlock": &shellCommand{
			Name:        "unlock",
			Description: "Unlock file on the disk",
			MinArgs:     1,
			MaxArgs:     1,
			Code:        shellUnlock,
			NeedsMount:  true,
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
				if commandTarget == nil {
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

	commandTarget = dsk
	commandPath := ""

	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 getPrompt(commandPath, commandTarget),
		HistoryFile:            binpath() + "/.shell_history",
		DisableAutoSaveHistory: false,
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
		fmt.Println("Error:", err.Error())
		return -1
	}

	commandTarget = dsk
	commandPath = ""

	return 0
}

func shellUnmount(args []string) int {

	if commandTarget != nil {

		commandTarget = nil
		commandPath = ""

		fmt.Println("Unmounted volume")

	}

	return 0
}

func shellHelp(args []string) int {

	keys := make([]string, 0)
	for k, _ := range commandList {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		info := commandList[k]
		fmt.Printf("%-10s %s\n", info.Name, info.Description)
	}

	return 0
}

func shellInfo(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	fmt.Printf("Disk path   : %s\n", fullpath)
	fmt.Printf("Disk type   : %s\n", commandTarget.Format.String())
	fmt.Printf("Sector Order: %s\n", commandTarget.Layout.String())
	fmt.Printf("Size        : %d bytes\n", len(commandTarget.Data))

	return 0
}

func shellQuit(args []string) int {

	return 999

}

func shellCat(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

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

	fmt.Printf("%-33s  %6s  %2s  %-23s  %s\n", "NAME", "BLOCKS", "RO", "KIND", "ADDITONAL")
	for _, f := range info.Files {
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

func shellAnalyze(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	info, err := analyze(0, fullpath)
	if err != nil {
		return -1
	}

	fmt.Printf("Format: %s\n", info.FormatID)
	fmt.Printf("Tracks: %d, Sectors: %d\n", info.Tracks, info.Sectors)

	return 0
}

func shellExtract(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	info, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	fmt.Println("Extract:", args[0])

	for _, f := range info.Files {

		if f.Filename == args[0] {
			err := ExtractFile(fullpath, f, true, true)
			if err == nil {
				fmt.Println("OK")
			} else {
				fmt.Println("FAILED")
				return -1
			}
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

	bpath := binpath() + "/backup" + path + "." + fts()
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

	fullpath, _ := filepath.Abs(commandTarget.Filename)

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

	if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		e := commandTarget.PRODOSCreateDirectory(path, name)
		if e != nil {
			fmt.Println(e)
			return -1
		}
		saveDisk(commandTarget, fullpath)
	} else {
		fmt.Println("Do not support Mkdir on " + commandTarget.Format.String() + " currently.")
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

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	data, err := ioutil.ReadFile(args[0])
	if err != nil {
		return -1
	}

	if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
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

		e := commandTarget.AppleDOSWriteFile(name, kind, data, int(addr))
		if e != nil {
			os.Stderr.WriteString("Failed to create file: " + e.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)

	} else if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
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

		e := commandTarget.PRODOSWriteFile(commandPath, name, kind, data, int(addr))
		if e != nil {
			os.Stderr.WriteString("Failed to create file: " + e.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)

	} else {
		os.Stderr.WriteString("Writing files not supported on " + commandTarget.Format.String())
		return -1
	}

	return 0

}

func shellDelete(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandTarget.AppleDOSDeleteFile(args[0])
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)

	} else if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		err = commandTarget.PRODOSDeleteFile(commandPath, args[0])
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)
	} else {
		os.Stderr.WriteString("Deleting files not supported on " + commandTarget.Format.String())
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

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandTarget.AppleDOSSetLocked(args[0], true)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)

	} else if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		err = commandTarget.PRODOSSetLocked(commandPath, args[0], true)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)
	} else {
		os.Stderr.WriteString("Locking files not supported on " + commandTarget.Format.String())
		return -1
	}

	return 0
}

func shellUnlock(args []string) int {

	fullpath, _ := filepath.Abs(commandTarget.Filename)

	_, err := analyze(0, fullpath)
	if err != nil {
		return 1
	}

	if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_DOS_SECTORS_13, disk.DF_DOS_SECTORS_16}) {
		err = commandTarget.AppleDOSSetLocked(args[0], false)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)

	} else if formatIn(commandTarget.Format.ID, []disk.DiskFormatID{disk.DF_PRODOS, disk.DF_PRODOS_800KB, disk.DF_PRODOS_400KB, disk.DF_PRODOS_CUSTOM}) {
		err = commandTarget.PRODOSSetLocked(commandPath, args[0], false)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return -1
		}
		saveDisk(commandTarget, fullpath)
	} else {
		os.Stderr.WriteString("Locking files not supported on " + commandTarget.Format.String())
		return -1
	}

	return 0
}
