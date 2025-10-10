package simplestdioplugin

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"slices"
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

	ctx        context.Context
	cancel     context.CancelFunc
	write_chan chan PluginComm
	resp_chan  chan PluginComm

	cmd_mutex sync.RWMutex
	cmd_map   map[string]CommandComm

	cmd      *exec.Cmd
	pipe_in  *os.File
	pipe_out *os.File
}

type PluginComm struct {
	id   []byte
	data []byte
}

type CommandComm struct {
	out chan []byte
	err chan []byte
}

func (comm CommandComm) close() {
	close(comm.out)
	close(comm.err)
}

func (plugin *PluginRunning) createChannel(id string) {
	plugin.cmd_mutex.Lock()
	defer plugin.cmd_mutex.Unlock()

	plugin.cmd_map[id] = CommandComm{out: make(chan []byte), err: make(chan []byte)}
}

func (plugin *PluginRunning) Command(command []byte) ([]byte, error) {
	if plugin.cmd.ProcessState != nil {
		return nil, errors.New("process is already exited")
	}

	id := uuid.New().String()

	plugin.createChannel(id)
	defer func() {
		plugin.cmd_mutex.Lock()
		defer plugin.cmd_mutex.Unlock()

		plugin.cmd_map[id].close()
	}()

	plugin.cmd_mutex.RLock()
	channels := plugin.cmd_map[id]
	plugin.cmd_mutex.RUnlock()

	plugin.write_chan <- PluginComm{id: []byte(id), data: command}

	select {
	case err := <-channels.err:
		return nil, errors.New("receive chan error:" + string(err))
	case result := <-channels.out:
		return result, nil
	}
}

func (plugin *PluginRunning) runner() error {
	for {
		select {
		case <-plugin.ctx.Done():
			return nil
		case comm := <-plugin.write_chan:
			plugin.cmd_mutex.RLock()
			if plugin.cmd_map[string(comm.id)].err == nil {
				return errors.New("channel err not exist")
			}
			plugin.cmd_mutex.RUnlock()

			encoded, err := EncodeCommand(comm.id, COMMAND_DATA, comm.data)
			if err != nil {
				plugin.cmd_mutex.Lock()
				plugin.cmd_map[string(comm.id)].err <- []byte(err.Error())
				plugin.cmd_map[string(comm.id)].close()
				plugin.cmd_mutex.Unlock()
			}

			_, err = plugin.pipe_in.Write(encoded)
			if err != nil {
				plugin.cmd_mutex.Lock()
				plugin.cmd_map[string(comm.id)].err <- []byte(err.Error())
				plugin.cmd_map[string(comm.id)].close()
				plugin.cmd_mutex.Unlock()
			}
		case comm := <-plugin.resp_chan:
			plugin.cmd_mutex.RLock()
			if plugin.cmd_map[string(comm.id)].out == nil {
				return errors.New("channel out not exist")
			}
			plugin.cmd_mutex.RUnlock()

			plugin.cmd_mutex.Lock()
			plugin.cmd_map[string(comm.id)].out <- comm.data
			plugin.cmd_mutex.Unlock()
		default:
			if plugin.cmd.ProcessState != nil {
				return errors.New("process exited")
			}
		}
	}
}

func (plugin *PluginRunning) reader() error {
	result := make(map[string][]byte)
	result_mutex := sync.Mutex{}
	for {
		select {
		case <-plugin.ctx.Done():
			return nil
		default:
			header := make([]byte, 6)
			n, err := plugin.pipe_out.Read(header)
			if err != nil {
				return errors.New("failed to read header: " + err.Error())
			}
			if n != 6 {
				return errors.New("invalid header packet length")
			}

			version := header[0]
			command := header[1]
			if version != 4 {
				return errors.New("invalid protocol version")
			}
			if command != byte(COMMAND_DATA) && command != byte(COMMAND_ERROR) {
				return errors.New("invalid protocol command")
			}

			length := binary.BigEndian.Uint32(header[2:])
			// 37 = uuid + separator + end byte
			if length < 37 {
				return errors.New("invalid response")
			}

			response := make([]byte, length+1)
			n, err = plugin.pipe_out.Read(response)
			if err != nil {
				return errors.New("failed to read data: " + err.Error())
			}
			if n != int(length)+1 {
				return errors.New("invalid data packet length")
			}

			id := response[0:36]
			resp := response[37:] // +1 separator

			if command == byte(COMMAND_ERROR) {
				result_mutex.Lock()
				final := PluginComm{id: id, data: resp[:len(resp)-1]}
				// reset after
				delete(result, string(id))
				result_mutex.Unlock()

				plugin.resp_chan <- final
			} else {
				if len(resp) == 1 {
					result_mutex.Lock()
					final := PluginComm{id: id, data: result[string(id)]}
					// reset after
					delete(result, string(id))
					result_mutex.Unlock()

					plugin.resp_chan <- final
				} else {
					result_mutex.Lock()
					result[string(id)] = append(result[string(id)], resp[:len(resp)-1]...)
					result_mutex.Unlock()
				}
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

func execPlugin(logger func(string), syncMap *sync.Map, location string, args ...string) error {
	name := path.Base(location)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, location, args...)

	pr1, pw1, err := os.Pipe()
	if err != nil {
		cancel()
		return err
	}

	pr2, pw2, err := os.Pipe()
	if err != nil {
		cancel()
		return err
	}

	pr3, pw3, err := os.Pipe()
	if err != nil {
		cancel()
		return err
	}

	cmd.Stdin = pr1
	cmd.Stdout = pw2
	cmd.Stderr = pw3

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	plugin_running := &PluginRunning{
		Name: name, Path: location, cmd: cmd, LogFunc: logger, ctx: ctx, cancel: cancel,
		cmd_mutex: sync.RWMutex{}, cmd_map: make(map[string]CommandComm),
		write_chan: make(chan PluginComm), resp_chan: make(chan PluginComm),
		pipe_in: pw1, pipe_out: pr2,
	}
	logger(fmt.Sprintf("started plugin %s (%s) pid: %d", name, location, cmd.Process.Pid))

	go func() {
		if err := plugin_running.runner(); err != nil {
			logger(fmt.Sprintf("plugin runner %s exited: %s", name, err.Error()))
		}
		cancel()
	}()

	go func() {
		if err := plugin_running.reader(); err != nil {
			logger(fmt.Sprintf("plugin reader %s exited: %s", name, err.Error()))
		}
		cancel()
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
		cancel()

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

type EncodedCommandType uint8

const (
	COMMAND_DATA  EncodedCommandType = 1
	COMMAND_ERROR EncodedCommandType = 2
)

// 04 01 0000 id ... 0xAD
// version-command-length-id-data-ending
func EncodeCommand(uuid []byte, command_type EncodedCommandType, data []byte) ([]byte, error) {
	if len(uuid) != 36 {
		return nil, fmt.Errorf("invalid uuid length")
	}

	var total int = 2 + 4 + len(uuid) + 1 + len(data) + 1
	if total > math.MaxInt32 {
		return nil, fmt.Errorf("command too long: %d bytes (max 2147483647)", total)
	}

	result := make([]byte, total)

	result[0] = 4                                                         // version
	result[1] = byte(command_type)                                        // type
	binary.BigEndian.PutUint32(result[2:], uint32(len(uuid)+len(data)+1)) // uuid + data length

	combined := append(uuid, []byte("-")...)
	combined = append(combined, data...)

	result = slices.Replace(result, 6, 6+len(uuid)+len(data)+1, combined...)

	result[len(result)-1] = 0xAD

	return result, nil
}

func NewPluginMap(log_func ...func(string)) PluginMap {
	data := PluginMap{Map: &sync.Map{}}
	if len(log_func) > 0 {
		data.LogFunc = log_func[0]
	}

	return data
}

func PluginRunner(config *PluginMap, base_location, extension string, args ...string) error {
	if config.LogFunc == nil {
		config.LogFunc = func(message string) {}
	}

	locations, err := findPluginPath(base_location, extension)
	if err != nil {
		return err
	}

	for _, val := range locations {
		if err := execPlugin(config.LogFunc, config.Map, val, args...); err != nil {
			return err
		}
	}

	for {
		config.Map.Range(func(key, value any) bool {
			p, ok := value.(*PluginRunning)
			if ok {
				if p.cmd.ProcessState != nil {
					_ = execPlugin(config.LogFunc, config.Map, p.Path, args...)
				}
			}

			return true
		})
		time.Sleep(time.Second)
	}
}
