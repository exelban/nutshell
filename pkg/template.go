package pkg

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Template struct {
	FS    fs.FS
	Debug bool

	List     *template.Template
	Details  *template.Template
	NotFound *template.Template
}

func (t *Template) Run(ctx context.Context) error {
	if err := t.loadTemplates(); err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	changeLog := make(map[string]chan bool)
	if err := filepath.Walk("template", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		ch, err := watchForFile(ctx, path)
		if err != nil {
			return fmt.Errorf("watch for file %s: %w", path, err)
		}
		changeLog[path] = ch
		return nil
	}); err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	for path, ch := range changeLog {
		go func(path string, ch chan bool) {
		loop:
			for {
				select {
				case <-ch:
					if err := t.loadTemplates(); err != nil {
						log.Printf("[ERROR] load templates: %v", err)
					} else {
						log.Printf("[DEBUG] reloaded %s", path)
					}
				case <-ctx.Done():
					close(ch)
					log.Printf("[DEBUG] watch for %s stopped", path)
					break loop
				}
			}
		}(path, ch)
	}

	if t.List == nil || t.Details == nil || t.NotFound == nil {
		return fmt.Errorf("templates not loaded")
	}

	return nil
}

func (t *Template) loadTemplates() error {
	filesystem := t.FS
	localFS := os.DirFS(".")
	if t.Debug {
		if _, err := fs.Stat(localFS, "template/list.html"); err == nil {
			filesystem = localFS
		}
	}

	templ, err := template.ParseFS(filesystem, "template/common/*.html", "template/*.html")
	if err != nil {
		return fmt.Errorf("parse files: %w", err)
	}

	t.List = templ.Lookup("list.html")
	t.Details = templ.Lookup("details.html")
	t.NotFound = templ.Lookup("404.html")

	return nil
}

func watchForFile(ctx context.Context, path string) (chan bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file not found %s: %v", path, err)
	}
	modTimestamp := fi.ModTime()
	ch := make(chan bool)

	go func() {
		tk := time.NewTicker(time.Second)
		for {
			select {
			case <-tk.C:
				fi, err = os.Stat(path)
				if err != nil {
					continue
				}
				if fi.ModTime() != modTimestamp {
					modTimestamp = fi.ModTime()
					ch <- true
				}
			case <-ctx.Done():
				tk.Stop()
				return
			}
		}
	}()

	return ch, nil
}
