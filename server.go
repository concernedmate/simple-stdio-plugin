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
	Map *sync.Map
}

func (mapped *PluginMap) GetPluginList() ([]*PluginRunning, error) {
	result := []*PluginRunning{}
	mapped.Map.Range(func(key, value any) bool {
		data, ok := value.(*PluginRunning)
		if ok {
			result = append(result, data)
		}
		return true
	})

	return result, nil
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
	Name string
	Path string

	ctx        context.Context
	cancel     context.CancelFunc
	write_chan chan PluginComm
	resp_chan  chan PluginComm

	cmd_mutex sync.RWMutex
	cmd_map   map[string]CommandComm

	cmd      *exec.Cmd
	pipe_in  io.WriteCloser
	pipe_out io.ReadCloser
	pipe_err io.ReadCloser
}

type PluginComm struct {
	id   []byte
	data []byte
}

type CommandComm struct {
	out chan []byte
	err chan []byte
}

func (comm CommandComm) Close() {
	close(comm.out)
	close(comm.err)
}

func (plugin *PluginRunning) CreateChannel(id string) {
	plugin.cmd_mutex.Lock()
	defer plugin.cmd_mutex.Unlock()

	plugin.cmd_map[id] = CommandComm{out: make(chan []byte), err: make(chan []byte)}
}

func (plugin *PluginRunning) Command(command []byte) ([]byte, error) {
	if plugin.cmd.ProcessState != nil {
		return nil, errors.New("process is already exited")
	}

	id := uuid.New().String()

	plugin.CreateChannel(id)
	defer func() {
		plugin.cmd_mutex.Lock()
		defer plugin.cmd_mutex.Unlock()

		plugin.cmd_map[id].Close()
	}()

	plugin.cmd_mutex.RLock()
	channels := plugin.cmd_map[id]
	plugin.cmd_mutex.RUnlock()

	plugin.write_chan <- PluginComm{id: []byte(id), data: command}

	select {
	case err := <-channels.err:
		return nil, errors.New(string(err))
	case result := <-channels.out:
		return result, nil
	}
}

func (plugin *PluginRunning) runner() error {
	defer plugin.cancel()

	for {
		select {
		case <-plugin.ctx.Done():
			return nil
		case comm := <-plugin.write_chan:
			if plugin.cmd_map[string(comm.id)].err == nil {
				return errors.New("channel err not exist")
			}

			encoded, err := EncodeCommand(comm.id, comm.data)
			if err != nil {
				plugin.cmd_mutex.Lock()
				plugin.cmd_map[string(comm.id)].err <- []byte(err.Error())
				plugin.cmd_map[string(comm.id)].Close()
				plugin.cmd_mutex.Unlock()
			}

			_, err = plugin.pipe_in.Write(encoded)
			if err != nil {
				plugin.cmd_mutex.Lock()
				plugin.cmd_map[string(comm.id)].err <- []byte(err.Error())
				plugin.cmd_map[string(comm.id)].Close()
				plugin.cmd_mutex.Unlock()
			}
		case comm := <-plugin.resp_chan:
			if plugin.cmd_map[string(comm.id)].out == nil {
				return errors.New("channel out not exist")
			}

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
	defer plugin.cancel()

	result := make(map[string][]byte)
	result_mutex := sync.Mutex{}
	for {
		select {
		case <-plugin.ctx.Done():
			return nil
		default:
			header := make([]byte, 5)
			if _, err := plugin.pipe_out.Read(header); err != nil {
				return err
			}

			length := binary.BigEndian.Uint32(header[1:])

			// 37 = uuid + separator + end byte
			if length < 37 {
				return errors.New("invalid response")
			}

			response := make([]byte, length+1)
			if _, err := plugin.pipe_out.Read(response); err != nil {
				return err
			}

			id := response[0:36]
			resp := response[37:] // +1 separator

			if len(resp) == 1 {
				result_mutex.Lock()
				final := PluginComm{id: id, data: result[string(id)]}
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

func execPlugin(syncMap *sync.Map, location string, args ...string) error {
	name := path.Base(location)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, location, args...)

	pipein, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	pipeout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	pipeerr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}

	plugin_running := &PluginRunning{
		Name: name, Path: location, cmd: cmd,
		ctx: ctx, cancel: cancel, cmd_mutex: sync.RWMutex{}, cmd_map: make(map[string]CommandComm),
		write_chan: make(chan PluginComm), resp_chan: make(chan PluginComm),
		pipe_in: pipein, pipe_out: pipeout, pipe_err: pipeerr,
	}
	syncMap.Store(name, plugin_running)

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	fmt.Printf("started plugin %s (%s) pid: %d \n", name, location, cmd.Process.Pid)

	go func() {
		if plugin_running.runner(); err != nil {
			fmt.Printf("plugin runner %s exited: %s\n", name, err.Error())
		}
		cancel()
	}()

	go func() {
		if plugin_running.reader(); err != nil {
			fmt.Printf("plugin reader %s exited: %s\n", name, err.Error())
		}
		cancel()
	}()

	go func() {
		if err := cmd.Wait(); err != nil {
			fmt.Printf("plugin %s exited: %s\n", name, err.Error())
		}
		cancel()

		close(plugin_running.resp_chan)
		close(plugin_running.write_chan)
	}()

	return nil
}

// 03 0000 id ... 0xAD
// version-length-id-data-ending
func EncodeCommand(uuid []byte, data []byte) ([]byte, error) {
	if len(uuid) != 36 {
		return nil, fmt.Errorf("invalid uuid length")
	}

	var total int = 1 + 4 + len(uuid) + 1 + len(data) + 1
	if total > math.MaxInt32 {
		return nil, fmt.Errorf("command too long: %d bytes (max 2147483647)", total)
	}

	result := make([]byte, total)

	result[0] = 3                                                         // version
	binary.BigEndian.PutUint32(result[1:], uint32(len(uuid)+len(data)+1)) // uuid + data length

	combined := append(uuid, []byte("-")...)
	combined = append(combined, data...)

	result = slices.Replace(result, 5, 5+len(uuid)+len(data)+1, combined...)
	result[len(result)-1] = 0xAD

	return result, nil
}

func NewPluginMap() PluginMap {
	return PluginMap{Map: &sync.Map{}}
}

func PluginRunner(sync *PluginMap, base_location, extension string, args ...string) error {
	locations, err := findPluginPath(base_location, extension)
	if err != nil {
		return err
	}

	for _, val := range locations {
		if err := execPlugin(sync.Map, val, args...); err != nil {
			return err
		}
	}

	for {
		sync.Map.Range(func(key, value any) bool {
			p, ok := value.(*PluginRunning)
			if ok {
				if p.cmd.ProcessState != nil {
					_ = execPlugin(sync.Map, p.Path, args...)
				}
			}

			return true
		})
		time.Sleep(time.Second)
	}
}
