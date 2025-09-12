package simplestdioplugin

import (
	"encoding/binary"
	"encoding/json"
	"errors"
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
	header := make([]byte, 6)
	if _, err := plugin.Stdin.Read(header); err != nil {
		return nil, nil, err
	}
	version := header[0]
	command := header[1]
	length := binary.BigEndian.Uint32(header[2:])

	if version != 4 {
		return nil, nil, errors.New("invalid protocol version")
	}
	if command != byte(COMMAND_DATA) && command != byte(COMMAND_ERROR) {
		return nil, nil, errors.New("invalid protocol command")
	}

	// 37 = uuid + separator + end byte
	if length < 37 {
		return nil, nil, errors.New("invalid response")
	}

	response := make([]byte, length+1) // plus ending
	_, err = plugin.Stdin.Read(response)
	if err != nil {
		return nil, nil, err
	}

	id = response[0:36]
	data = response[37:] // +1 separator

	data = data[:len(data)-1]

	return id, data, nil
}

func (plugin *PluginData) writeOutput(id []byte, data []byte) error {
	counter := 0
	for {
		if counter >= len(data) {
			break
		}

		var chunk []byte
		if counter+CHUNK_SIZE >= len(data) {
			chunk = data[counter:]
		} else {
			chunk = data[counter:(counter + CHUNK_SIZE)]
		}
		counter += CHUNK_SIZE

		total, err := EncodeCommand(id, COMMAND_DATA, chunk)
		if err != nil {
			return err
		}
		_, err = plugin.Stdout.Write(total)
		if err != nil {
			return err
		}
	}

	eof, err := EncodeCommand(id, COMMAND_DATA, []byte{})
	if err != nil {
		return err
	}
	_, err = plugin.Stdout.Write(eof)
	if err != nil {
		return err
	}

	return nil
}

func (plugin *PluginData) writeError(id []byte, data string) error {
	bytes := []byte(data)

	if len(bytes) > CHUNK_SIZE {
		bytes = bytes[:CHUNK_SIZE]
	}

	total, err := EncodeCommand(id, COMMAND_ERROR, bytes)
	if err != nil {
		return err
	}
	_, err = plugin.Stdout.Write(total)
	if err != nil {
		return err
	}

	return nil
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
