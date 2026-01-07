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
	Name    string
	Path    string
	LogFunc func(message string)

	write_chan chan PluginComm
	resp_chan  chan PluginComm

	cmd_mutex sync.RWMutex
	cmd_map   map[string]chan CommandComm

	cmd      *exec.Cmd
	pipe_in  *os.File
	pipe_out *os.File
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

	bytes, err := EncodeMessage(input)
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

			if err := WriteAll(comm.id, comm.data, plugin.pipe_in); err != nil {
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
			read, err := ReadChunk(plugin.pipe_out)
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
		Name: name, Path: location, cmd: cmd, LogFunc: logger,
		cmd_mutex: sync.RWMutex{}, cmd_map: make(map[string]chan CommandComm),
		write_chan: make(chan PluginComm), resp_chan: make(chan PluginComm),
		pipe_in: pw1, pipe_out: pr2,
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
		buffer := make([]byte, 10)

		result := []byte{}
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := pr3.Read(buffer)
				if err != nil {
					if err == io.EOF {
						logger(fmt.Sprintf("plugin %s stderr: \n%s", name, string(result)))
						return
					}
					logger(fmt.Sprintf("plugin %s failed to read stderr: %s", name, err.Error()))
				}

				result = append(result, buffer[0:n]...)
				if n < 10 {
					logger(fmt.Sprintf("plugin %s stderr: \n%s", name, string(result)))
					return
				}
			}
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

	for {
		select {
		case <-ctx.Done():
			return nil
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
}

func PluginRunner(config *PluginMap, base_location, extension string, args ...string) error {
	ctx := context.Background()
	if err := pluginRoutine(ctx, config, base_location, extension, args...); err != nil {
		return err
	}
	return nil
}

func PluginRunnerWithContext(ctx context.Context, config *PluginMap, base_location, extension string, args ...string) error {
	if err := pluginRoutine(ctx, config, base_location, extension, args...); err != nil {
		return err
	}
	return nil
}
