package cli

import (
	"fmt"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/mcuadros/ofelia/core"
)

// TriggerCommand daemon process
type TriggerCommand struct {
	ConfigFile         string   `long:"config" description:"configuration file" default:"/etc/ofelia.conf"`
	DockerLabelsConfig bool     `short:"d" long:"docker" description:"read configurations from docker labels"`
	DockerFilters      []string `short:"f" long:"docker-filter" description:"filter to select docker containers"`

	config    *Config
	scheduler *core.Scheduler
	signals   chan os.Signal
	done      chan bool
}

// Execute runs the daemon
func (c *TriggerCommand) Execute(args []string) error {
	_, err := os.Stat("/.dockerenv")
	IsDockerEnv = !os.IsNotExist(err)

	trigger := args[0]

	if trigger == "" {
		return fmt.Errorf("Trigger job not found")
	}

	if err := c.boot(); err != nil {
		return err
	}

	c.setSignals()
	c.scheduler.Logger.Debugf("Looking for job: %v", trigger)
	c.scheduler.RunJobByName(trigger)

	close(c.done)
	if err := c.shutdown(); err != nil {
		return err
	}

	return nil
}

func (c *TriggerCommand) boot() (err error) {
	if c.DockerLabelsConfig {
		c.scheduler, err = BuildFromDockerLabels(c.DockerFilters...)
	} else {
		c.scheduler, err = BuildFromFile(c.ConfigFile)
	}

	return
}

func (c *TriggerCommand) setSignals() {
	c.signals = make(chan os.Signal, 1)
	c.done = make(chan bool, 1)

	signal.Notify(c.signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-c.signals
		c.scheduler.Logger.Warningf(
			"Signal received: %s, shutting down the process\n", sig,
		)

		c.done <- true
	}()
}

func (c *TriggerCommand) shutdown() error {
	<-c.done
	if !c.scheduler.IsRunning() {
		return nil
	}

	c.scheduler.Logger.Warningf("Waiting running jobs.")
	return c.scheduler.Stop()
}
