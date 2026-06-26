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
	Router map[string]func(data []byte) ([]byte, error)

	resp_chan      chan ReadResult
	error_chan     chan ReadResult
	heartbeat_chan chan struct{}

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) reader(ctx context.Context) error {
	result := make(map[string][]byte)
	result_mutex := sync.Mutex{}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			read, err := readChunk(plugin.Stdin)
			if err != nil {
				return err
			}

			switch read.command {
			case COMMAND_DATA:
				result_mutex.Lock()
				result[string(read.uuid)] = append(result[string(read.uuid)], read.data[:len(read.data)-1]...)
				result_mutex.Unlock()
			case COMMAND_ERROR:
				result_mutex.Lock()
				final := ReadResult{uuid: read.uuid, data: read.data[:len(read.data)-1], command: read.command}
				// reset after
				delete(result, string(read.uuid))
				result_mutex.Unlock()

				plugin.error_chan <- final
			case COMMAND_FINAL:
				result_mutex.Lock()
				final := ReadResult{uuid: read.uuid, data: result[string(read.uuid)], command: read.command}
				// reset after
				delete(result, string(read.uuid))
				result_mutex.Unlock()

				plugin.resp_chan <- final
			case COMMAND_HEARTBEAT:
				plugin.heartbeat_chan <- struct{}{}
			default:
				return errors.New("invalid command")
			}
		}
	}
}

func (plugin *PluginData) writeOutput(id []byte, data []byte) error {
	return writeAll(id, data, plugin.Stdout)
}

func (plugin *PluginData) writeError(id []byte, data string) error {
	return writeAll(id, []byte(data), plugin.Stdout)
}

func NewPluginClient(Router map[string]func(json []byte) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{
		Router:         Router,
		Stdin:          Stdin,
		Stdout:         Stdout,
		resp_chan:      make(chan ReadResult),
		error_chan:     make(chan ReadResult),
		heartbeat_chan: make(chan struct{}),
	}
}

func PluginServe(plugin PluginData, max_conn ...int) error {
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
			_ = plugin.writeError([]byte(uuid.New().String()), "reader error: "+err.Error())
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
		case <-plugin.heartbeat_chan:
			timer.Reset(5 * time.Second)
		case data := <-plugin.error_chan:
			_ = plugin.writeError(data.uuid, string(data.data))
		case data := <-plugin.resp_chan:
			mut.RLock()
			if concurrent >= max_concurrent {
				_ = plugin.writeError(data.uuid, "concurrent error: MAX CONCURRENT command reached")
				mut.RUnlock()
				continue
			}
			mut.RUnlock()

			input, err := decodeMessage(data.data)
			if err != nil {
				_ = plugin.writeError(data.uuid, fmt.Sprintf("decode message error: %v", err))
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

				function := plugin.Router[input.Function]
				if function != nil {
					result, err := function(input.Data)
					if err != nil {
						_ = plugin.writeError(data.uuid, "error func: "+err.Error())
					} else {
						if err := plugin.writeOutput(data.uuid, result); err != nil {
							_ = plugin.writeError(data.uuid, "error write: "+err.Error())
						}
					}
				} else {
					_ = plugin.writeError(data.uuid, "error func: "+input.Function+" not found")
				}
			}()

		}
	}
}
