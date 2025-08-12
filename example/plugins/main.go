package main

import (
	"log"
	"os"
	simplestdioplugin "simple-stdio-plugin"
)

func main() {
	plugin := simplestdioplugin.NewPlugin(nil, os.Stdin, os.Stdout)
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
