package cli

import (
	"github.com/netresearch/ofelia/core"
	"github.com/netresearch/ofelia/middlewares"

	defaults "github.com/mcuadros/go-defaults"
	gcfg "gopkg.in/gcfg.v1"
)

const (
	logFormat     = "%{time} %{color} %{shortfile} ▶ %{level}%{color:reset} %{message}"
	jobExec       = "job-exec"
	jobRun        = "job-run"
	jobServiceRun = "job-service-run"
	jobLocal      = "job-local"
)

// Config contains the configuration
type Config struct {
	Global struct {
		middlewares.SlackConfig `mapstructure:",squash"`
		middlewares.SaveConfig  `mapstructure:",squash"`
		middlewares.MailConfig  `mapstructure:",squash"`
	}
	ExecJobs      map[string]*ExecJobConfig    `gcfg:"job-exec" mapstructure:"job-exec,squash"`
	RunJobs       map[string]*RunJobConfig     `gcfg:"job-run" mapstructure:"job-run,squash"`
	ServiceJobs   map[string]*RunServiceConfig `gcfg:"job-service-run" mapstructure:"job-service-run,squash"`
	LocalJobs     map[string]*LocalJobConfig   `gcfg:"job-local" mapstructure:"job-local,squash"`
	Docker        DockerConfig
	sh            *core.Scheduler
	dockerHandler *DockerHandler
	logger        core.Logger
}

func NewConfig(logger core.Logger) *Config {
	c := &Config{
		ExecJobs:    make(map[string]*ExecJobConfig),
		RunJobs:     make(map[string]*RunJobConfig),
		ServiceJobs: make(map[string]*RunServiceConfig),
		LocalJobs:   make(map[string]*LocalJobConfig),
		logger:      logger,
	}

	defaults.SetDefaults(c)
	return c
}

// BuildFromFile builds a scheduler using the config from a file
func BuildFromFile(filename string, logger core.Logger) (*Config, error) {
	c := NewConfig(logger)
	err := gcfg.ReadFileInto(c, filename)
	return c, err
}

// BuildFromString builds a scheduler using the config from a string
func BuildFromString(config string, logger core.Logger) (*Config, error) {
	c := NewConfig(logger)
	if err := gcfg.ReadStringInto(c, config); err != nil {
		return nil, err
	}
	return c, nil
}

// Call this only once at app init
func (c *Config) InitializeApp() error {
	c.sh = core.NewScheduler(c.logger)
	c.buildSchedulerMiddlewares(c.sh)

	var err error
	c.dockerHandler, err = NewDockerHandler(c, c.logger, c.Docker.Filters)
	if err != nil {
		return err
	}

	// In order to support non dynamic job types such as Local or Run using labels
	// lets parse the labels and merge the job lists
	dockerLabels, err := c.dockerHandler.GetDockerLabels()
	if err == nil {
		parsedLabelConfig := Config{}

		parsedLabelConfig.buildFromDockerLabels(dockerLabels)
		for name, j := range parsedLabelConfig.RunJobs {
			c.RunJobs[name] = j
		}

		for name, j := range parsedLabelConfig.LocalJobs {
			c.LocalJobs[name] = j
		}

		for name, j := range parsedLabelConfig.ServiceJobs {
			c.ServiceJobs[name] = j
		}
	}

	for name, j := range c.ExecJobs {
		defaults.SetDefaults(j)
		j.Client = c.dockerHandler.GetInternalDockerClient()
		j.Name = name
		j.buildMiddlewares()
		c.sh.AddJob(j)
	}

	for name, j := range c.RunJobs {
		defaults.SetDefaults(j)
		j.Client = c.dockerHandler.GetInternalDockerClient()
		j.Name = name
		j.buildMiddlewares()
		c.sh.AddJob(j)
	}

	for name, j := range c.LocalJobs {
		defaults.SetDefaults(j)
		j.Name = name
		j.buildMiddlewares()
		c.sh.AddJob(j)
	}

	for name, j := range c.ServiceJobs {
		defaults.SetDefaults(j)
		j.Name = name
		j.Client = c.dockerHandler.GetInternalDockerClient()
		j.buildMiddlewares()
		c.sh.AddJob(j)
	}

	return nil
}

func (c *Config) buildSchedulerMiddlewares(sh *core.Scheduler) {
	sh.Use(middlewares.NewSlack(&c.Global.SlackConfig))
	sh.Use(middlewares.NewSave(&c.Global.SaveConfig))
	sh.Use(middlewares.NewMail(&c.Global.MailConfig))
}

func (c *Config) dockerLabelsUpdate(labels map[string]map[string]string) {
	// log start of func
	c.logger.Debugf("dockerLabelsUpdate started")

	// Get the current labels
	var parsedLabelConfig Config
	parsedLabelConfig.buildFromDockerLabels(labels)
	c.logger.Debugf("dockerLabelsUpdate labels: %v", labels)

	var hasIterated bool

	// Calculate the delta execJobs
	hasIterated = false
	for name, j := range c.ExecJobs {
		hasIterated = true
		c.logger.Debugf("checking exec job %s for changes", name)
		found := false
		for newJobsName, newJob := range parsedLabelConfig.ExecJobs {
			c.logger.Debugf("checking exec job %s vs %s", name, newJobsName)
			// Check if the schedule has changed
			if name == newJobsName {
				found = true
				// There is a slight race condition were a job can be canceled / restarted with different params
				// so, lets take care of it by simply restarting
				// For the hash to work properly, we must fill the fields before calling it
				defaults.SetDefaults(newJob)
				newJob.Client = c.dockerHandler.GetInternalDockerClient()
				newJob.Name = newJobsName
				if newJob.Hash() != j.Hash() {
					// Remove from the scheduler
					c.sh.RemoveJob(j)
					// Add the job back to the scheduler
					newJob.buildMiddlewares()
					c.sh.AddJob(newJob)
					// Update the job config
					c.ExecJobs[name] = newJob
				}
				break
			}
		}
		if !found {
			// Remove from the scheduler
			c.sh.RemoveJob(j)
			// Remove the job from the ExecJobs map
			delete(c.ExecJobs, name)
			c.logger.Debugf("removing exec job %s", name)
		}
	}
	if !hasIterated {
		c.logger.Debugf("no exec jobs to update")
	}

	// Check for aditions
	hasIterated = false
	for newJobsName, newJob := range parsedLabelConfig.ExecJobs {
		hasIterated = true
		c.logger.Debugf("checking exec job %s if new", newJobsName)
		found := false
		for name := range c.ExecJobs {
			if name == newJobsName {
				found = true
				break
			}
		}
		if !found {
			defaults.SetDefaults(newJob)
			newJob.Client = c.dockerHandler.GetInternalDockerClient()
			newJob.Name = newJobsName
			newJob.buildMiddlewares()
			c.sh.AddJob(newJob)
			c.ExecJobs[newJobsName] = newJob
		}
	}
	if !hasIterated {
		c.logger.Debugf("no new exec jobs")
	}

	hasIterated = false
	for name, j := range c.RunJobs {
		hasIterated = true
		c.logger.Debugf("checking run job %s for changes", name)
		found := false
		for newJobsName, newJob := range parsedLabelConfig.RunJobs {
			// Check if the schedule has changed
			if name == newJobsName {
				found = true
				// There is a slight race condition were a job can be canceled / restarted with different params
				// so, lets take care of it by simply restarting
				// For the hash to work properly, we must fill the fields before calling it
				defaults.SetDefaults(newJob)
				newJob.Client = c.dockerHandler.GetInternalDockerClient()
				newJob.Name = newJobsName
				if newJob.Hash() != j.Hash() {
					// Remove from the scheduler
					c.sh.RemoveJob(j)
					// Add the job back to the scheduler
					newJob.buildMiddlewares()
					c.sh.AddJob(newJob)
					// Update the job config
					c.RunJobs[name] = newJob
				}
				break
			}
		}
		if !found {
			// Remove from the scheduler
			c.sh.RemoveJob(j)
			// Remove the job from the RunJobs map
			delete(c.RunJobs, name)
			c.logger.Debugf("removing run job %s", name)
		}
	}
	if !hasIterated {
		c.logger.Debugf("no run jobs to update")
	}

	// Check for aditions
	hasIterated = false
	for newJobsName, newJob := range parsedLabelConfig.RunJobs {
		hasIterated = true
		c.logger.Debugf("checking run job %s if new", newJobsName)
		found := false
		for name := range c.RunJobs {
			if name == newJobsName {
				found = true
				break
			}
		}
		if !found {
			defaults.SetDefaults(newJob)
			newJob.Client = c.dockerHandler.GetInternalDockerClient()
			newJob.Name = newJobsName
			newJob.buildMiddlewares()
			c.sh.AddJob(newJob)
			c.RunJobs[newJobsName] = newJob
		}
	}
	if !hasIterated {
		c.logger.Debugf("no new run jobs")
	}
}

// ExecJobConfig contains all configuration params needed to build a ExecJob
type ExecJobConfig struct {
	core.ExecJob              `mapstructure:",squash"`
	middlewares.OverlapConfig `mapstructure:",squash"`
	middlewares.SlackConfig   `mapstructure:",squash"`
	middlewares.SaveConfig    `mapstructure:",squash"`
	middlewares.MailConfig    `mapstructure:",squash"`
}

func (c *ExecJobConfig) buildMiddlewares() {
	c.ExecJob.Use(middlewares.NewOverlap(&c.OverlapConfig))
	c.ExecJob.Use(middlewares.NewSlack(&c.SlackConfig))
	c.ExecJob.Use(middlewares.NewSave(&c.SaveConfig))
	c.ExecJob.Use(middlewares.NewMail(&c.MailConfig))
}

// RunServiceConfig contains all configuration params needed to build a RunJob
type RunServiceConfig struct {
	core.RunServiceJob        `mapstructure:",squash"`
	middlewares.OverlapConfig `mapstructure:",squash"`
	middlewares.SlackConfig   `mapstructure:",squash"`
	middlewares.SaveConfig    `mapstructure:",squash"`
	middlewares.MailConfig    `mapstructure:",squash"`
}

type RunJobConfig struct {
	core.RunJob               `mapstructure:",squash"`
	middlewares.OverlapConfig `mapstructure:",squash"`
	middlewares.SlackConfig   `mapstructure:",squash"`
	middlewares.SaveConfig    `mapstructure:",squash"`
	middlewares.MailConfig    `mapstructure:",squash"`
}

func (c *RunJobConfig) buildMiddlewares() {
	c.RunJob.Use(middlewares.NewOverlap(&c.OverlapConfig))
	c.RunJob.Use(middlewares.NewSlack(&c.SlackConfig))
	c.RunJob.Use(middlewares.NewSave(&c.SaveConfig))
	c.RunJob.Use(middlewares.NewMail(&c.MailConfig))
}

// LocalJobConfig contains all configuration params needed to build a RunJob
type LocalJobConfig struct {
	core.LocalJob             `mapstructure:",squash"`
	middlewares.OverlapConfig `mapstructure:",squash"`
	middlewares.SlackConfig   `mapstructure:",squash"`
	middlewares.SaveConfig    `mapstructure:",squash"`
	middlewares.MailConfig    `mapstructure:",squash"`
}

func (c *LocalJobConfig) buildMiddlewares() {
	c.LocalJob.Use(middlewares.NewOverlap(&c.OverlapConfig))
	c.LocalJob.Use(middlewares.NewSlack(&c.SlackConfig))
	c.LocalJob.Use(middlewares.NewSave(&c.SaveConfig))
	c.LocalJob.Use(middlewares.NewMail(&c.MailConfig))
}

func (c *RunServiceConfig) buildMiddlewares() {
	c.RunServiceJob.Use(middlewares.NewOverlap(&c.OverlapConfig))
	c.RunServiceJob.Use(middlewares.NewSlack(&c.SlackConfig))
	c.RunServiceJob.Use(middlewares.NewSave(&c.SaveConfig))
	c.RunServiceJob.Use(middlewares.NewMail(&c.MailConfig))
}

type DockerConfig struct {
	Filters []string `mapstructure:"filters"`
}
