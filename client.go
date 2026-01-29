package simplestdioplugin

import (
	"os"
	"sync"
)

type PluginData struct {
	Router map[string]func(data []byte) ([]byte, error)

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) readInput() (id []byte, data []byte, err error) {
	result, err := readAll(plugin.Stdin)
	if err != nil {
		return nil, nil, err
	}
	return result.uuid, result.data, nil
}

func (plugin *PluginData) writeOutput(id []byte, data []byte) error {
	return writeAll(id, data, plugin.Stdout)
}

func (plugin *PluginData) writeError(id []byte, data string) error {
	return writeAll(id, []byte(data), plugin.Stdout)
}

func NewPluginClient(Router map[string]func(json []byte) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{Router: Router, Stdin: Stdin, Stdout: Stdout}
}

func PluginServe(plugin PluginData, max_conn ...int) error {
	max_concurrent := 1000
	if len(max_conn) > 0 {
		max_concurrent = max_conn[0]
	}

	mut := sync.RWMutex{}
	concurrent := 0

	for {
		id, data, err := plugin.readInput()
		if err != nil {
			_ = plugin.writeError(id, "readInput error: "+err.Error())
			continue
		}

		mut.RLock()
		if concurrent >= max_concurrent {
			_ = plugin.writeError(id, "concurrent error: MAX CONCURRENT command reached")
			mut.RUnlock()
			continue
		}
		mut.RUnlock()

		input, err := decodeMessage(data)
		if err != nil {
			_ = plugin.writeError(id, "decode message error: "+err.Error())
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
					_ = plugin.writeError(id, "error func: "+err.Error())
				} else {
					if err := plugin.writeOutput(id, result); err != nil {
						_ = plugin.writeError(id, "error write: "+err.Error())
					}
				}
			} else {
				_ = plugin.writeError(id, "error func: "+input.Function+" not found")
			}
		}()
	}
}
