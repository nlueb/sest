package main

import (
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"path"
	"regexp"
	"text/template"
	"time"

	"github.com/radovskyb/watcher"
	"gopkg.in/yaml.v3"
)

var (
	configPath        string
	templateFunctions template.FuncMap
)

type config struct {
	Input struct {
		Files       []string
		Directories []string
		Filter      string
	}
	Events map[string]struct {
		Src         string
		Dest        string
		EventType   string `yaml:"event_type"`
		ChannelName string `yaml:"channel_name"`
	}
}

func (cfg *config) resolveRelativePaths() {
	configDir := path.Dir(configPath)
	for i, filename := range cfg.Input.Files {
		if path.IsAbs(filename) {
			continue
		}
		cfg.Input.Files[i] = path.Join(configDir, filename)
	}

	for i, dirName := range cfg.Input.Directories {
		if path.IsAbs(dirName) {
			continue
		}
		cfg.Input.Directories[i] = path.Join(configDir, dirName)
	}

	for key, event := range cfg.Events {
		if path.IsAbs(event.Dest) {
			continue
		}
		event.Dest = path.Join(configDir, event.Dest)
		cfg.Events[key] = event
	}
}

type event struct {
	Regex       *regexp.Regexp
	Template    []byte
	EventType   string
	ChannelName string
}

func init() {
	log.SetFlags(log.Lshortfile | log.Lmicroseconds)
	configPath = getEnvOrDefault("SEST_CONFIG_PATH", "/etc/sest/config.yml")
	templateFunctions = template.FuncMap{
		"timestamp": getCurrentTimestamp,
	}
}

func main() {
	cfg := loadConfig(configPath)
	cfg.resolveRelativePaths()

	watcher := createWatcher(cfg)
	events := createEventList(cfg)
	logFiles := createLogFileList(cfg)

	for key, _ := range logFiles {
		log.Println(key)
	}

	go eventLoop(watcher, events, logFiles)

	if err := watcher.Start(time.Millisecond * 100); err != nil {
		log.Fatalln(err)
	}
}

func eventLoop(w *watcher.Watcher, events []event, files map[string]*LogFile) {
	for {
		select {
		case event := <-w.Event:
			if event.Op == watcher.Write {
				handleWrite(events, files[event.Path])
			}
		case err := <-w.Error:
			log.Fatalln(err)
		case <-w.Closed:
			return
		}
	}
}

func handleWrite(events []event, file *LogFile) {
	if file == nil {
		log.Println("Got event, but no file")
		return
	}
	log.Printf("Old offset: %d", file.GetOffset())
	lines, _ := file.ReadNewLines()
	log.Printf("New offset: %d", file.GetOffset())
	for _, event := range events {
		log.Printf("Looking for event: %s", event.EventType)
		for _, submatches := range event.Regex.FindAllSubmatchIndex(lines, -1) {
			log.Println("Found event")
			step := event.Regex.Expand([]byte{}, event.Template, lines, submatches)
			t, err := template.New("test").Funcs(templateFunctions).Parse(string(step))
			if err != nil {
				log.Println(err)
				continue
			}
			var tpl bytes.Buffer
			t.Execute(&tpl, nil)
			log.Println(tpl.String())
		}
	}
}

func getEnvOrDefault(key, defaultVal string) (value string) {
	var ok bool
	if value, ok = os.LookupEnv(key); !ok {
		value = defaultVal
	}
	return
}

func loadConfig(filename string) config {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
	}

	c := config{}

	err = yaml.Unmarshal(content, &c)
	if err != nil {
		log.Fatal(err)
	}

	return c
}

func createWatcher(cfg config) *watcher.Watcher {
	w := watcher.New()

	w.FilterOps(watcher.Write)

	if cfg.Input.Filter != "" {
		re, err := regexp.Compile(cfg.Input.Filter)
		if err != nil {
			log.Printf("Could not compile input filter: %s with error: %v", cfg.Input.Filter, err)
		} else {
			w.AddFilterHook(watcher.RegexFilterHook(re, false))
		}
	}

	for _, filename := range cfg.Input.Files {
		w.Add(filename)
	}

	for _, directory := range cfg.Input.Directories {
		w.Add(directory)
	}

	return w
}

func createEventList(cfg config) []event {
	if len(cfg.Events) <= 0 {
		return nil
	}
	events := make([]event, 0, len(cfg.Events))
	for key, eventCfg := range cfg.Events {
		re, err := regexp.Compile(eventCfg.Src)
		if err != nil {
			log.Printf("Could not compile regex (%s) for event %s", eventCfg.Src, key)
			continue
		}

		template, err := ioutil.ReadFile(eventCfg.Dest)
		if err != nil {
			log.Printf("Could not load template %s for event %s", eventCfg.Dest, key)
			continue
		}

		event := event{
			Regex:       re,
			Template:    template,
			EventType:   eventCfg.EventType,
			ChannelName: eventCfg.ChannelName,
		}
		events = append(events, event)
	}
	return events
}

func createLogFileList(cfg config) map[string]*LogFile {
	logFiles := make(map[string]*LogFile)

	filenames := make([]string, len(cfg.Input.Files))
	copy(filenames, cfg.Input.Files)

	for _, path := range cfg.Input.Directories {
		files, err := getFilesFromDir(path)
		if err != nil {
			continue
		}
		filenames = append(filenames, files...)
	}

	re, err := regexp.Compile(cfg.Input.Filter)
	if err != nil {
		log.Printf("Could not compile input filter: %s with error: %v", cfg.Input.Filter, err)
	} else {
		filenames = filter(filenames, re.MatchString)
	}

	for _, filename := range filenames {
		logFile, err := NewLogFile(filename, 0)
		if err != nil {
			log.Printf("Could not watch file %s with error: %v", filename, err)
			continue
		}
		logFiles[filename] = logFile
	}

	return logFiles
}

func getFilesFromDir(dirPath string) ([]string, error) {
	entries, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	files := []string{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, path.Join(dirPath, entry.Name()))
	}

	return files, nil
}

func filter(vs []string, f func(string) bool) []string {
	vsf := make([]string, 0)
	for _, v := range vs {
		if f(v) {
			vsf = append(vsf, v)
		}
	}
	return vsf
}

func getCurrentTimestamp() string {
	return time.Now().Format("2006-01-02T15:04:05-0700")
}
