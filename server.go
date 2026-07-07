package simplestdioplugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PluginRunning struct {
	Name string
	Path string

	log_func func(message string)
	router   map[string]func(data []byte) ([]byte, error)

	resp_mutex  sync.RWMutex
	resp_map    map[string]chan ReadResult
	req_c       chan ReadResult
	heartbeat_c chan struct{}

	cmd      *exec.Cmd
	pipe_in  *os.File
	pipe_out *os.File
	pipe_err *os.File
}

// Command call function from plugin and return its result
func (plugin *PluginRunning) Command(input MessageInput, timeout ...time.Duration) ([]byte, error) {
	if plugin.cmd.ProcessState != nil {
		return nil, fmt.Errorf("process is already exited")
	}

	bytes, err := encodeMessage(input)
	if err != nil {
		return nil, fmt.Errorf("failed to encode MessageInput: %v", err)
	}

	id := uuid.New().String()
	resp_c := make(chan ReadResult)

	plugin.resp_mutex.Lock()
	plugin.resp_map[id] = resp_c
	plugin.resp_mutex.Unlock()
	defer func() {
		plugin.resp_mutex.Lock()
		defer plugin.resp_mutex.Unlock()

		close(plugin.resp_map[id])
		delete(plugin.resp_map, id)
	}()

	if err := writeAll([]byte(id), bytes, COMMAND_REQUEST, plugin.pipe_in); err != nil {
		return nil, fmt.Errorf("failed to write request: %v", err)
	}

	if len(timeout) > 0 {
		timer := time.NewTimer(timeout[0])
		defer timer.Stop()

		select {
		case <-timer.C:
			return nil, fmt.Errorf("command timed out")
		case result := <-resp_c:
			if result.command == COMMAND_ERROR {
				return nil, fmt.Errorf("%s", string(result.data))
			}
			return result.data, nil
		}
	} else {
		result := <-resp_c
		if result.command == COMMAND_ERROR {
			return nil, fmt.Errorf("%s", string(result.data))
		}
		return result.data, nil
	}
}

func (plugin *PluginRunning) runner(ctx context.Context) error {
	max_concurrent := 1000

	mut := sync.RWMutex{}
	concurrent := 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case data := <-plugin.req_c:
			mut.RLock()
			if concurrent >= max_concurrent {
				_ = writeAll(data.uuid, []byte("error: MAX CONCURRENT command reached"), COMMAND_ERROR, plugin.pipe_in)
				mut.RUnlock()
				continue
			}
			mut.RUnlock()

			input, err := decodeMessage(data.data)
			if err != nil {
				_ = writeAll(data.uuid, []byte(fmt.Sprintf("error decode message: %v", err)), COMMAND_ERROR, plugin.pipe_in)
				continue
			}

			mut.Lock()
			concurrent += 1
			mut.Unlock()
			go func() {
				defer func() {
					mut.Lock()
					concurrent -= 1
					mut.Unlock()
				}()

				if function := plugin.router[input.Function]; function != nil {
					result, err := function(input.Data)
					if err != nil {
						_ = writeAll(data.uuid, []byte(fmt.Sprintf("%v", err)), COMMAND_ERROR, plugin.pipe_in)
					} else {
						if err := writeAll(data.uuid, result, COMMAND_RESULT, plugin.pipe_in); err != nil {
							_ = writeAll(data.uuid, []byte(fmt.Sprintf("error write: %v", err)), COMMAND_ERROR, plugin.pipe_in)
						}
					}
				} else {
					_ = writeAll(data.uuid, []byte("error func: "+input.Function+" not found"), COMMAND_ERROR, plugin.pipe_in)
				}
			}()
		case <-plugin.heartbeat_c:
			if err := writeHeartbeat(plugin.pipe_in); err != nil {
				return err
			}
		}
	}
}

func (plugin *PluginRunning) reader(ctx context.Context) error {
	result := make(map[string][]byte)
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
			case COMMAND_DATA:
				result[string(read.uuid)] = append(result[string(read.uuid)], read.data...)
			case COMMAND_REQUEST:
				plugin.req_c <- ReadResult{uuid: read.uuid, command: read.command, data: result[string(read.uuid)]}
				delete(result, string(read.uuid))
			case COMMAND_ERROR:
				plugin.resp_mutex.RLock()
				resp_c, ok := plugin.resp_map[string(read.uuid)]
				plugin.resp_mutex.RUnlock()

				if ok && resp_c != nil {
					resp_c <- ReadResult{uuid: read.uuid, command: read.command, data: result[string(read.uuid)]}
				}
				delete(result, string(read.uuid))
			case COMMAND_RESULT:
				plugin.resp_mutex.RLock()
				resp_c, ok := plugin.resp_map[string(read.uuid)]
				plugin.resp_mutex.RUnlock()

				if ok && resp_c != nil {
					resp_c <- ReadResult{uuid: read.uuid, command: read.command, data: result[string(read.uuid)]}
				}
				delete(result, string(read.uuid))
			default:
				return fmt.Errorf("invalid command")
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
