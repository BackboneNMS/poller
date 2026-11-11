package config

import (
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Dashboard struct {
		URL     string        `yaml:"url"`
		Token   string        `yaml:"token"`
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"dashboard"`
	SNMP struct {
		Concurrency int           `yaml:"concurrency"`
		Timeout     time.Duration `yaml:"timeout"`
		Retries     int           `yaml:"retries"`
	} `yaml:"snmp"`
}

func New() *Config {
	c := &Config{}

	c.Dashboard.Timeout = 30 * time.Second
	c.SNMP.Concurrency = 32
	c.SNMP.Timeout = 5 * time.Second
	c.SNMP.Retries = 1

	return c
}

func (c *Config) ReadFile(file string) error {
	data, err := os.ReadFile(file)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err == nil {
		if err = yaml.Unmarshal(data, c); err != nil {
			return err
		}
	}

	c.readEnvVars()

	return c.validate()
}

func (c *Config) readEnvVars() {
	if v := os.Getenv("DASHBOARD_URL"); len(v) > 0 {
		c.Dashboard.URL = v
	}

	if v := os.Getenv("DASHBOARD_TOKEN"); len(v) > 0 {
		c.Dashboard.Token = v
	}

	c.Dashboard.URL = strings.TrimRight(c.Dashboard.URL, "/")
}

func (c *Config) validate() error {
	if len(c.Dashboard.URL) < 1 {
		return errors.New("dashboard.url (or DASHBOARD_URL) is required")
	}

	if len(c.Dashboard.Token) < 1 {
		return errors.New("dashboard.token (or DASHBOARD_TOKEN) is required, generate one from the Pollers page of the dashboard")
	}

	if c.Dashboard.Timeout <= 0 {
		return errors.New("dashboard.timeout must be greater than 0")
	}

	if c.SNMP.Concurrency < 1 {
		return errors.New("snmp.concurrency must be at least 1")
	}

	if c.SNMP.Timeout <= 0 {
		return errors.New("snmp.timeout must be greater than 0")
	}

	if c.SNMP.Retries < 0 {
		return errors.New("snmp.retries must not be negative")
	}

	return nil
}
