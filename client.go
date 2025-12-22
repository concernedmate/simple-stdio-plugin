package simplestdioplugin

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

const CHUNK_SIZE = 3072

type PluginData struct {
	Router map[string]func(json []byte) ([]byte, error)

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) readInput() (id []byte, data []byte, err error) {
	result, err := ReadAll(plugin.Stdin)
	if err != nil {
		return nil, nil, err
	}
	return result.uuid, result.data, nil
}

func (plugin *PluginData) writeOutput(id []byte, data []byte) error {
	return WriteAll(id, data, plugin.Stdout)
}

func (plugin *PluginData) writeError(id []byte, data string) error {
	return WriteAll(id, []byte(data), plugin.Stdout)
}

func parseClientMessage(msg string) (sub string, jsondata []byte) {
	if strings.Contains(msg, "?json=") {
		split := strings.Split(msg, "?json=")

		sub = split[0]
		jsondata = []byte(strings.Join(split[1:], "?json="))
		if json.Valid(jsondata) {
			return sub, jsondata
		}

		return sub, nil
	}

	return msg, nil
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
			_ = plugin.writeError(id, "error: "+err.Error())
			continue
		}

		mut.RLock()
		if concurrent >= max_concurrent {
			_ = plugin.writeError(id, "error: MAX CONCURRENT command reached")
			mut.RUnlock()
			continue
		}
		mut.RUnlock()

		sub, json := parseClientMessage(string(data))

		mut.Lock()
		concurrent += 1
		mut.Unlock()
		go func() {
			defer func() {
				mut.Lock()
				concurrent -= 1
				mut.Unlock()
			}()

			function := plugin.Router[sub]
			if function != nil {
				result, err := function(json)
				if err != nil {
					_ = plugin.writeError(id, "error func: "+err.Error())
				} else {
					if err := plugin.writeOutput(id, result); err != nil {
						_ = plugin.writeError(id, "error write: "+err.Error())
					}
				}
			} else {
				_ = plugin.writeError(id, "error sub: "+sub+" not found")
			}
		}()
	}
}
