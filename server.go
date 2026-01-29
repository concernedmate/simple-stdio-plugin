package simplestdioplugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	childmanager "github.com/concernedmate/simple-stdio-plugin/child_manager"
	"github.com/google/uuid"
)

type PluginMap struct {
	Map     *sync.Map
	LogFunc func(message string)
}

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

type PluginRunning struct {
	Name     string
	Path     string
	LogFunc  func(message string)
	KillFunc func() error

	write_chan chan PluginComm
	resp_chan  chan PluginComm

	cmd_mutex sync.RWMutex
	cmd_map   map[string]chan CommandComm

	cmd      *exec.Cmd
	pipe_in  *os.File
	pipe_out *os.File
	pipe_err *os.File
}

type PluginComm struct {
	id   []byte
	data []byte
}

type CommandComm struct {
	out []byte
	err []byte
}

func (plugin *PluginRunning) Command(input MessageInput) ([]byte, error) {
	if plugin.cmd.ProcessState != nil {
		return nil, errors.New("process is already exited")
	}

	bytes, err := encodeMessage(input)
	if err != nil {
		return nil, fmt.Errorf("failed to encode MessageInput: %s", err.Error())
	}

	id := uuid.New().String()

	plugin.cmd_mutex.Lock()
	plugin.cmd_map[id] = make(chan CommandComm)
	plugin.cmd_mutex.Unlock()
	defer func() {
		plugin.cmd_mutex.Lock()
		defer plugin.cmd_mutex.Unlock()

		close(plugin.cmd_map[id])
	}()

	plugin.cmd_mutex.RLock()
	channels := plugin.cmd_map[id]
	plugin.cmd_mutex.RUnlock()

	plugin.write_chan <- PluginComm{id: []byte(id), data: bytes}

	result := <-channels
	if result.err != nil {
		return nil, errors.New(string(result.err))
	}
	return result.out, nil
}

func (plugin *PluginRunning) runner(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case comm := <-plugin.write_chan:
			plugin.cmd_mutex.RLock()
			if plugin.cmd_map[string(comm.id)] == nil {
				return errors.New("channel not exist")
			}
			plugin.cmd_mutex.RUnlock()

			if err := writeAll(comm.id, comm.data, plugin.pipe_in); err != nil {
				return err
			}
		case comm := <-plugin.resp_chan:
			plugin.cmd_mutex.RLock()
			if plugin.cmd_map[string(comm.id)] == nil {
				return errors.New("channel out not exist")
			}
			plugin.cmd_mutex.RUnlock()

			plugin.cmd_mutex.Lock()
			plugin.cmd_map[string(comm.id)] <- CommandComm{out: comm.data, err: nil}
			plugin.cmd_mutex.Unlock()
		}
	}
}

func (plugin *PluginRunning) reader(ctx context.Context) error {
	result := make(map[string][]byte)
	result_mutex := sync.Mutex{}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			read, err := readChunk(plugin.pipe_out)
			if err != nil {
				return err
			}

			switch read.command {
			case COMMAND_ERROR:
				result_mutex.Lock()
				final := PluginComm{id: read.uuid, data: read.data[:len(read.data)-1]}
				// reset after
				delete(result, string(read.uuid))
				result_mutex.Unlock()

				plugin.resp_chan <- final
			case COMMAND_DATA:
				result_mutex.Lock()
				result[string(read.uuid)] = append(result[string(read.uuid)], read.data[:len(read.data)-1]...)
				result_mutex.Unlock()
			case COMMAND_FINAL:
				result_mutex.Lock()
				final := PluginComm{id: read.uuid, data: result[string(read.uuid)]}
				// reset after
				delete(result, string(read.uuid))
				result_mutex.Unlock()

				plugin.resp_chan <- final
			default:
				return errors.New("invalid command")
			}
		}
	}
}

func (plugin *PluginRunning) stderr(ctx context.Context) error {
	buffer := make([]byte, 10)

	result := []byte{}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			n, err := plugin.pipe_err.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return fmt.Errorf("plugin %s stderr: \n%s", plugin.Name, string(result))
				}
				return fmt.Errorf("plugin %s failed to read stderr: %s", plugin.Name, err.Error())
			}

			result = append(result, buffer[0:n]...)
			if n < 10 {
				return fmt.Errorf("plugin %s stderr: \n%s", plugin.Name, string(result))
			}
		}
	}
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

func execPlugin(ctx context.Context, logger func(string), syncMap *sync.Map, location string, args ...string) error {
	name := path.Base(location)

	cmd := exec.CommandContext(ctx, location, args...)
	childmanager.ConfigureCommand(cmd)

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

	if err := childmanager.AddChildProcess(cmd.Process); err != nil {
		cmd.Process.Kill()
		return err
	}

	plugin_running := &PluginRunning{
		Name: name, Path: location, cmd: cmd, LogFunc: logger,
		cmd_mutex: sync.RWMutex{}, cmd_map: make(map[string]chan CommandComm),
		write_chan: make(chan PluginComm), resp_chan: make(chan PluginComm),
		pipe_in: pw1, pipe_out: pr2, pipe_err: pr3,
	}
	logger(fmt.Sprintf("started plugin %s (%s) pid: %d", name, location, cmd.Process.Pid))

	go func() {
		if err := plugin_running.runner(ctx); err != nil {
			logger(fmt.Sprintf("plugin runner %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.reader(ctx); err != nil {
			logger(fmt.Sprintf("plugin reader %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.stderr(ctx); err != nil {
			logger(err.Error())
		}
	}()
	go func() {
		if err := cmd.Wait(); err != nil {
			logger(fmt.Sprintf("plugin %s exited: %s", name, err.Error()))
		}

		// cleanup
		pr1.Close()
		pw1.Close()

		pr2.Close()
		pw2.Close()

		close(plugin_running.resp_chan)
		close(plugin_running.write_chan)
	}()

	syncMap.Store(name, plugin_running)
	return nil
}

func NewPluginMap(log_func ...func(string)) PluginMap {
	data := PluginMap{Map: &sync.Map{}}
	if len(log_func) > 0 {
		data.LogFunc = log_func[0]
	}

	return data
}

func pluginRoutine(ctx context.Context, config *PluginMap, base_location, extension string, args ...string) error {
	if err := childmanager.InitializeChildProcessManager(); err != nil {
		return err
	}

	if config.LogFunc == nil {
		config.LogFunc = func(message string) {}
	}

	locations, err := findPluginPath(base_location, extension)
	if err != nil {
		return err
	}

	for _, val := range locations {
		if err := execPlugin(ctx, config.LogFunc, config.Map, val, args...); err != nil {
			return err
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				childmanager.DisposeChildProcessManager()
				return
			default:
				config.Map.Range(func(key, value any) bool {
					p, ok := value.(*PluginRunning)
					if ok {
						if p.cmd.ProcessState != nil {
							_ = execPlugin(ctx, config.LogFunc, config.Map, p.Path, args...)
						}
					}

					return true
				})
				time.Sleep(time.Second)
			}
		}
	}()

	return nil
}

type StartPluginConfig struct {
	Ctx       context.Context
	BaseDir   string
	Extension string

	LogFunc func(string)
}

func StartPlugin(config StartPluginConfig, args ...string) (*PluginMap, error) {
	if config.Ctx == nil {
		return nil, errors.New("invalid context")
	}
	mapped := NewPluginMap(config.LogFunc)
	ptr := &mapped

	if err := pluginRoutine(config.Ctx, ptr, config.BaseDir, config.Extension, args...); err != nil {
		return nil, err
	}
	return ptr, nil
}
