//go:build linux

package simplestdioplugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"sync"
)

func execPlugin(ctx context.Context, logger func(string), syncMap *sync.Map, location string, args ...string) error {
	name := path.Base(location)

	cmd := exec.CommandContext(ctx, location, args...)
	pr1, pw1, err := os.Pipe()
	if err != nil {
		return err
	}
	pr2, pw2, err := os.Pipe()
	if err != nil {
		return err
	}
	pr3, pw3, err := os.Pipe()
	if err != nil {
		return err
	}

	cmd.Stdin = pr1
	cmd.Stdout = pw2
	cmd.Stderr = pw3

	if err := cmd.Start(); err != nil {
		return err
	}

	plugin_running := &PluginRunning{
		Name:           name,
		Path:           location,
		cmd:            cmd,
		LogFunc:        logger,
		cmd_mutex:      sync.RWMutex{},
		cmd_map:        make(map[string]chan CommandComm),
		write_chan:     make(chan PluginComm),
		resp_chan:      make(chan PluginComm),
		heartbeat_chan: make(chan struct{}),
		pipe_in:        pw1,
		pipe_out:       pr2,
		pipe_err:       pr3,
	}
	logger(fmt.Sprintf("started plugin %s (%s) pid: %d", name, location, cmd.Process.Pid))

	go func() {
		if err := plugin_running.runner(ctx); err != nil {
			logger(fmt.Sprintf("plugin runner %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.reader(ctx); err != nil {
			logger(fmt.Sprintf("plugin reader %s exited: %s", name, err.Error()))
		}
	}()
	go func() {
		if err := plugin_running.stderr(ctx); err != nil {
			logger(err.Error())
		}
	}()
	go func() {
		if err := cmd.Wait(); err != nil {
			logger(fmt.Sprintf("plugin %s exited: %s", name, err.Error()))
		}

		// cleanup
		pr1.Close()
		pw1.Close()

		pr2.Close()
		pw2.Close()

		pr3.Close()
		pw3.Close()

		close(plugin_running.resp_chan)
		close(plugin_running.write_chan)
		close(plugin_running.heartbeat_chan)
	}()

	syncMap.Store(name, plugin_running)
	return nil
}
