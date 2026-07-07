package simplestdioplugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type PluginData struct {
	router map[string]func(data []byte) ([]byte, error)

	resp_mutex  sync.RWMutex
	resp_map    map[string]chan ReadResult
	req_c       chan ReadResult
	heartbeat_c chan struct{}

	stdin  *os.File
	stdout *os.File
}

type PluginClientConfig struct {
	// Router is functions that can be called from host side
	Router map[string]func(data []byte) ([]byte, error)

	// Stdin is file descriptor for plugin input, defaults to os.Stdin
	Stdin *os.File
	// Stdout is file descriptor for plugin output, defaults to os.Stdout
	Stdout *os.File
}

// Command call function from host and return its result
func (plugin *PluginData) Command(input MessageInput, timeout ...time.Duration) ([]byte, error) {
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

	if err := writeAll([]byte(id), bytes, COMMAND_REQUEST, plugin.stdout); err != nil {
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

func (plugin *PluginData) reader(ctx context.Context) error {
	result := make(map[string][]byte)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			read, err := readChunk(plugin.stdin)
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
			case COMMAND_HEARTBEAT:
				plugin.heartbeat_c <- struct{}{}
			default:
				return errors.New("invalid command")
			}
		}
	}
}

func NewPluginClient(config PluginClientConfig) *PluginData {
	stdin := config.Stdin
	stdout := config.Stdout

	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}

	return &PluginData{
		router: config.Router,

		resp_mutex:  sync.RWMutex{},
		resp_map:    make(map[string]chan ReadResult),
		req_c:       make(chan ReadResult),
		heartbeat_c: make(chan struct{}),

		stdin:  stdin,
		stdout: stdout,
	}
}

func PluginServe(plugin *PluginData, max_conn ...int) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	max_concurrent := 1000
	if len(max_conn) > 0 {
		max_concurrent = max_conn[0]
	}

	mut := sync.RWMutex{}
	concurrent := 0

	// reader
	go func() {
		if err := plugin.reader(ctx); err != nil {
			_ = writeAll([]byte(uuid.New().String()), []byte("reader error: "+err.Error()), COMMAND_ERROR, plugin.stdout)
		}
		cancel()
	}()

	var timer = time.NewTimer(5 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("plugin closed (reader errored)")
		case <-timer.C:
			cancel()
			return fmt.Errorf("plugin closed (heartbeat timeout)")
		case <-plugin.heartbeat_c:
			timer.Reset(5 * time.Second)
		case data := <-plugin.req_c:
			mut.RLock()
			if concurrent >= max_concurrent {
				_ = writeAll(data.uuid, []byte("error concurrent: MAX CONCURRENT command reached"), COMMAND_ERROR, plugin.stdout)
				mut.RUnlock()
				continue
			}
			mut.RUnlock()

			input, err := decodeMessage(data.data)
			if err != nil {
				_ = writeAll(data.uuid, []byte(fmt.Sprintf("error decode message: %v", err)), COMMAND_ERROR, plugin.stdout)
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
						_ = writeAll(data.uuid, []byte("error func: "+err.Error()), COMMAND_ERROR, plugin.stdout)
					} else {
						if err := writeAll(data.uuid, result, COMMAND_RESULT, plugin.stdout); err != nil {
							_ = writeAll(data.uuid, []byte("error write: "+err.Error()), COMMAND_ERROR, plugin.stdout)
						}
					}
				} else {
					_ = writeAll(data.uuid, []byte("error func: "+input.Function+" not found"), COMMAND_ERROR, plugin.stdout)
				}
			}()
		}
	}
}
