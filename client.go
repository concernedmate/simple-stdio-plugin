package simplestdioplugin

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
)

const CHUNK_SIZE = 4096

type PluginData struct {
	Router map[string]func(json []byte) ([]byte, error)

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) readInput() ([]byte, error) {
	header := make([]byte, 5)
	if _, err := plugin.Stdin.Read(header); err != nil {
		return nil, err
	}

	// version := header[0]
	length := int(binary.BigEndian.Uint32(header[1:]))

	data := make([]byte, length+1) // plus ending
	if _, err := plugin.Stdin.Read(data); err != nil {
		return nil, err
	}
	data = data[:len(data)-1]

	return data, nil
}

func (plugin *PluginData) writeOutput(data []byte) error {
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

		total, err := EncodeCommand(chunk)
		if err != nil {
			return err
		}
		_, err = plugin.Stdout.Write(total)
		if err != nil {
			return err
		}
	}

	eof, err := EncodeCommand([]byte{})
	if err != nil {
		return err
	}
	_, err = plugin.Stdout.Write(eof)
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

func NewPlugin(Router map[string]func(json []byte) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{Router: Router, Stdin: Stdin, Stdout: Stdout}
}

func PluginServe(plugin PluginData) error {
	for {
		data, err := plugin.readInput()
		if err != nil {
			return err
		}

		sub, json := parseClientMessage(string(data))
		function := plugin.Router[sub]
		if function != nil {
			result, err := function(json)
			if err != nil {
				_ = plugin.writeOutput([]byte("plugin error: " + err.Error()))
			} else {
				if err := plugin.writeOutput(result); err != nil {
					plugin.writeOutput([]byte("plugin error: " + err.Error()))
				}
			}
		} else {
			plugin.writeOutput([]byte("plugin error: not found"))
		}
	}
}
