package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/c-bata/go-prompt"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

const (
	VERSION    = "0.0.1"
	TimeLayout = "2006/1/2"
)

type config struct {
	DataFilePath string `toml:"data_filepath"`
	SelectCmd    string `toml:"select_cmd"`
}

type Entries []Entry

// @<Date>
// #<Tags>
// !<Priority> from 1 to 5
type Entry struct {
	Task     string
	Date     time.Time
	Tags     []string
	Priority int
}

var commands = []cli.Command{
	{
		Name:    "add",
		Aliases: []string{"a"},
		Usage:   "Add task",
		Action:  cmdAdd,
	},
	{
		Name:    "delete",
		Aliases: []string{"d"},
		Usage:   "Delete task",
		Action:  cmdDelete,
	},
	{
		Name:    "list",
		Aliases: []string{"l"},
		Usage:   "Show task list",
		Action:  cmdList,
	},
}

func (cfg *config) load() error {
	var dir string
	dir = filepath.Join(os.Getenv("HOME"), ".config", "gli")
	if err := os.MkdirAll(dir, 700); err != nil {
		return fmt.Errorf("cannot create directory: %v", err)
	}
	file := filepath.Join(dir, "config.toml")

	_, err := os.Stat(file)
	if err == nil {
		_, err := toml.DecodeFile(file, cfg)
		if err != nil {
			return err
		}
		cfg.DataFilePath = expandPath(cfg.DataFilePath)
		return nil
	}

	if !os.IsNotExist(err) {
		return err
	}

	f, err := os.Create(file)
	if err != nil {
		return err
	}
	cfg.DataFilePath = filepath.Join(dir, "data.gob")
	cfg.SelectCmd = "fzf"
	return toml.NewEncoder(f).Encode(cfg)
}

func expandPath(s string) string {
	if len(s) >= 2 && s[0] == '~' && os.IsPathSeparator(s[1]) {
		if runtime.GOOS == "windows" {
			s = filepath.Join(os.Getenv("USERPROFILE"), s[2:])
		} else {
			s = filepath.Join(os.Getenv("HOME"), s[2:])
		}
	}
	return os.Expand(s, os.Getenv)
}

func (es *Entries) read(file string) error {
	f, err := os.OpenFile(file, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	return gob.NewDecoder(f).Decode(&es)
}

func (es *Entries) write(file string) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	return gob.NewEncoder(f).Encode(es)
}

func (es Entries) erase(i int) Entries {
	if i < len(es)-1 {
		copy(es[i:], es[i+1:])
	}
	es[len(es)-1] = Entry{}
	return es[:len(es)-1]
}

func cmdAdd(c *cli.Context) error {
	var cfg config
	err := cfg.load()
	if err != nil {
		return err
	}

	f := func(in prompt.Document) []prompt.Suggest {
		s := []prompt.Suggest{
			{Text: "@", Description: "Set a deadline. The format is YYYYY/mm/dd"},
			{Text: "#", Description: "Set a Tags."},
			{Text: "!", Description: "Set a Priority. From 1 to 5"},
		}
		return prompt.FilterHasPrefix(s, in.GetWordBeforeCursor(), true)
	}
	in := prompt.Input(">>> ", f,
		prompt.OptionPrefixTextColor(prompt.Yellow))
	args := strings.Split(strings.Trim(in, " "), " ")

	var e Entry
	for _, a := range args {
		if len(a) == 0 {
			continue
		}
		switch a[0] {
		case '#':
			e.Tags = append(e.Tags, a[1:])
		case '@':
			t, err := time.Parse(TimeLayout, a[1:])
			if err != nil {
				return errors.Wrap(err, "Time format is yyyy/mm/dd")
			}
			e.Date = t
		case '!':
			if e.Priority == 0 {
				p, err := strconv.Atoi(a[1:])
				if err != nil {
					return errors.Wrap(err, "Priority is a number")
				}
				e.Priority = p
			}
		default:
			if e.Task == "" {
				e.Task = a
			} else {
				e.Task += " " + a
			}
		}
	}

	if e.Task == "" {
		return errors.New("Cannot register task. Enter a task name.")
	}
	if e.Date.IsZero() {
		e.Date = time.Now()
	}

	var es Entries
	err = es.read(cfg.DataFilePath)
	if err != nil {
		return err
	}
	es = append(es, e)
	return es.write(cfg.DataFilePath)
}

func cmdDelete(c *cli.Context) error {
	var cfg config
	err := cfg.load()
	if err != nil {
		return err
	}

	var es Entries
	err = es.read(cfg.DataFilePath)
	if err != nil {
		return err
	}

	var tbuf bytes.Buffer
	table := tablewriter.NewWriter(&tbuf)
	table.SetAutoWrapText(false)
	table.SetBorders(tablewriter.Border{
		Left: false, Top: false, Right: false, Bottom: false})
	for _, e := range es {
		date := e.Date.Format(TimeLayout)
		tags := strings.Join(e.Tags, ",")
		priority := strconv.Itoa(e.Priority)
		table.Append([]string{e.Task, date, tags, priority})
	}
	table.Render()
	ts := strings.Split(tbuf.String(), "\n")

	var buf bytes.Buffer
	cmd := exec.Command("sh", "-c", cfg.SelectCmd)
	cmd.Stderr = os.Stderr
	cmd.Stdout = &buf
	cmd.Stdin = &tbuf
	err = cmd.Run()
	if err != nil {
		return err
	}

	if buf.Len() == 0 {
		return errors.New("No files selected")
	}
	target := strings.Trim(buf.String(), "\n")
	for i, ss := range ts {
		res := strings.Index(ss, target)
		if res != -1 {
			es = es.erase(i)
			// [TODO] When deleting duplicate items, delete only one
			break
		}
	}
	return es.write(cfg.DataFilePath)
}

func cmdList(c *cli.Context) error {
	var cfg config
	err := cfg.load()
	if err != nil {
		return err
	}

	var es Entries
	err = es.read(cfg.DataFilePath)
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Task", "Date", "Tags", "Priority"})
	table.SetRowLine(true)
	for _, e := range es {
		date := e.Date.Format(TimeLayout)
		tags := strings.Join(e.Tags, ",")
		priority := strconv.Itoa(e.Priority)
		table.Append([]string{e.Task, date, tags, priority})
	}
	table.Render()
	return nil
}

func msg(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[0], err)
		return 1
	}
	return 0
}

func run() int {
	app := cli.NewApp()
	app.Name = "gli"
	app.Usage = "Todo list implemented with golang"
	app.Version = VERSION
	app.Commands = commands

	return msg(app.Run(os.Args))
}

func main() {
	os.Exit(run())
}
