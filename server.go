package simplestdioplugin

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
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

	Cmd     *exec.Cmd
	PipeIn  io.WriteCloser
	PipeOut io.ReadCloser
	PipeErr io.ReadCloser
}

func (plugin *PluginRunning) Command(command []byte) ([]byte, error) {
	if plugin.Cmd.ProcessState != nil {
		return nil, errors.New("process is already exited")
	}

	encoded, err := EncodeCommand(command)
	if err != nil {
		return nil, err
	}

	output_channel := make(chan []byte)
	error_channel := make(chan []byte)
	go func(plugin *PluginRunning, output_chan, error_chan chan []byte) {
		defer close(output_chan)
		defer close(error_chan)

		if plugin == nil {
			log.Fatal("PluginRunning is NULL")
		}

		_, err = plugin.PipeIn.Write(encoded)
		if err != nil {
			error_chan <- []byte(fmt.Sprintf("invalid command: %s", err.Error()))
			return
		}

		header := make([]byte, 5)
		if _, err := plugin.PipeOut.Read(header); err != nil {
			error_chan <- []byte(fmt.Sprintf("error command: %s", err.Error()))
			return
		}

		length := binary.BigEndian.Uint32(header[1:])

		result := make([]byte, length+1)
		if _, err := plugin.PipeOut.Read(result); err != nil {
			error_chan <- []byte(fmt.Sprintf("error command: %s", err.Error()))
			return
		}

		result = result[:len(result)-1]
		output_chan <- result
	}(plugin, output_channel, error_channel)

	select {
	case err := <-error_channel:
		{
			return nil, errors.New(string(err))
		}
	case result := <-output_channel:
		{
			return result, nil
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

func execPlugin(ctx context.Context, location string, syncMap *sync.Map) error {
	name := path.Base(location)

	cmd := exec.CommandContext(ctx, location)

	pipein, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	pipeout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	pipeerr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	plugin_running := &PluginRunning{
		Name: name, Path: location, Cmd: cmd,
		PipeIn: pipein, PipeOut: pipeout, PipeErr: pipeerr,
	}
	syncMap.Store(name, plugin_running)

	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("started plugin %s (%s) pid: %d \n", name, location, cmd.Process.Pid)

	go func() {
		if err := cmd.Wait(); err != nil {
			fmt.Printf("plugin %s exited: %s\n", name, err.Error())
		}
	}()

	return nil
}

// 02 0000 ... 0xAD
// version-length-data-ending
func EncodeCommand(data []byte) ([]byte, error) {
	var total int = 1 + 4 + len(data) + 1
	if total > math.MaxInt32 {
		return nil, fmt.Errorf("command too long: %d bytes (max 2147483647)", total)
	}

	result := make([]byte, total)

	result[0] = 2                                             // version
	binary.BigEndian.PutUint32(result[1:], uint32(len(data))) // data length

	fmt.Println(hex.EncodeToString(result))

	result = slices.Replace(result, 5, 5+len(data), data...)

	result[len(result)-1] = 0xAD

	fmt.Println(hex.EncodeToString(result))

	return result, nil
}

func PluginRunner(ctx context.Context, sync *PluginMap, base_location, extension string) error {
	locations, err := findPluginPath(base_location, extension)
	if err != nil {
		return err
	}

	for _, val := range locations {
		if err := execPlugin(ctx, val, sync.Map); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			sync.Map.Range(func(key, value any) bool {
				p, ok := value.(*PluginRunning)
				if ok {
					if p.Cmd.ProcessState != nil {
						_ = execPlugin(ctx, p.Path, sync.Map)
					}
				}

				return true
			})
			time.Sleep(time.Second)
		}
	}
}
