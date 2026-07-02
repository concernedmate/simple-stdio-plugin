package simplestdioplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

type execPluginInput struct {
	location string
	logger   func(string)
	router   map[string]func(data []byte) ([]byte, error)
	args     []string
}

type StartPluginConfig struct {
	// BaseDir is base location to look for plugin
	BaseDir string
	// Extension is plugin file extension
	Extension string
	// LogFunc is function that is used to log plugin debug message, keep nil to disable
	LogFunc func(string)
	// Router is functions that can be called from plugin side
	Router map[string]func(data []byte) ([]byte, error)
}

type PluginMap struct {
	Map *sync.Map
}

// GetPluginNames list available plugins name
func (mapped *PluginMap) GetPluginNames() []string {
	result := []string{}
	mapped.Map.Range(func(key, value any) bool {
		data, ok := value.(*PluginRunning)
		if ok {
			result = append(result, data.Name)
		}
		return true
	})

	return result
}

// GetPluginByName returns plugin instance by name
func (mapped *PluginMap) GetPluginByName(plugin_name string) (*PluginRunning, error) {
	val, ok := mapped.Map.Load(plugin_name)
	if !ok {
		return nil, errors.New("plugin not found")
	}
	result, ok := val.(*PluginRunning)
	if !ok {
		return nil, errors.New("plugin not found")
	}

	return result, nil
}

func findPluginPath(base_location string, extension string) ([]string, error) {
	dirs, err := os.ReadDir(base_location)
	if err != nil {
		return nil, err
	}

	result := []string{}
	for _, val := range dirs {
		if !val.IsDir() && strings.Contains(val.Name(), "."+extension) {
			result = append(result, path.Join(base_location, val.Name()))
		}
	}

	return result, nil
}

func pluginRoutine(ctx context.Context, config StartPluginConfig, plugin_map *PluginMap, args ...string) error {
	if config.LogFunc == nil {
		config.LogFunc = func(message string) {}
	}

	locations, err := findPluginPath(config.BaseDir, config.Extension)
	if err != nil {
		return err
	}

	for _, val := range locations {
		if err := execPlugin(ctx, plugin_map.Map, execPluginInput{
			location: val,
			logger:   config.LogFunc,
			router:   config.Router,
			args:     args,
		}); err != nil {
			return err
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				plugin_map.Map.Range(func(key, value any) bool {
					p, ok := value.(*PluginRunning)
					if ok {
						if p.cmd.ProcessState != nil {
							_ = execPlugin(ctx, plugin_map.Map, execPluginInput{
								location: p.Path,
								logger:   p.log_func,
								router:   p.router,
								args:     p.cmd.Args,
							})
						} else {
							p.heartbeat_c <- struct{}{}
						}
					}

					return true
				})

				time.Sleep(3 * time.Second)
			}
		}
	}()

	return nil
}

func execPlugin(ctx context.Context, syncMap *sync.Map, input execPluginInput) error {
	name := path.Base(input.location)

	cmd := commandContext(ctx, input.location, input.args...)
	pr1, pw1, err := os.Pipe()
	if err != nil {
		return err
	}
	pr2, pw2, err := os.Pipe()
	if err != nil {
		return err
	}
	pr3, pw3, err := os.Pipe()
	if err != nil {
		return err
	}

	cmd.Stdin = pr1
	cmd.Stdout = pw2
	cmd.Stderr = pw3

	if err := cmd.Start(); err != nil {
		return err
	}

	plugin_running := &PluginRunning{
		Name:     name,
		Path:     input.location,
		log_func: input.logger,
		router:   input.router,

		resp_mutex:  sync.RWMutex{},
		resp_map:    make(map[string]chan ReadResult),
		req_c:       make(chan ReadResult),
		heartbeat_c: make(chan struct{}),

		cmd:      cmd,
		pipe_in:  pw1,
		pipe_out: pr2,
		pipe_err: pr3,
	}
	input.logger(fmt.Sprintf("started plugin %s (%s) pid: %d", name, input.location, cmd.Process.Pid))

	go func() {
		if err := plugin_running.runner(ctx); err != nil {
			input.logger(fmt.Sprintf("plugin runner %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.reader(ctx); err != nil {
			input.logger(fmt.Sprintf("plugin reader %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.stderr(ctx); err != nil {
			input.logger(err.Error())
		}
	}()
	go func() {
		if err := cmd.Wait(); err != nil {
			input.logger(fmt.Sprintf("plugin %s exited: %s", name, err.Error()))
		}

		// cleanup
		pr1.Close()
		pw1.Close()

		pr2.Close()
		pw2.Close()

		pr3.Close()
		pw3.Close()

		close(plugin_running.req_c)
		close(plugin_running.heartbeat_c)
	}()

	syncMap.Store(name, plugin_running)
	return nil
}

// StartPlugin search for available plugins and runs it
func StartPlugin(ctx context.Context, config StartPluginConfig, args ...string) (*PluginMap, error) {
	plugin_map := &PluginMap{Map: &sync.Map{}}

	if err := pluginRoutine(ctx, config, plugin_map, args...); err != nil {
		return nil, err
	}
	return plugin_map, nil
}
