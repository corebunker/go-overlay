package main

import (
	"fmt"
	"io"

	toml "github.com/pelletier/go-toml/v2"
)

type Timeouts struct {
	PostScript      int `toml:"post_script_timeout,omitempty"`
	ServiceShutdown int `toml:"service_shutdown_timeout,omitempty"`
	GlobalShutdown  int `toml:"global_shutdown_timeout,omitempty"`
	DependencyWait  int `toml:"dependency_wait_timeout,omitempty"`
}

type DependsOnField []string

func (d *DependsOnField) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case string:
		*d = []string{v}
	case []interface{}:
		deps := make([]string, len(v))
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				return fmt.Errorf("depends_on array must contain only strings")
			}
			deps[i] = str
		}
		*d = deps
	default:
		return fmt.Errorf("depends_on must be a string or array of strings")
	}
	return nil
}

type WaitAfterField struct {
	PerDep   map[string]int
	Global   int
	IsPerDep bool
}

func (w *WaitAfterField) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case int64:
		w.Global = int(v)
		w.IsPerDep = false
	case map[string]interface{}:
		w.PerDep = make(map[string]int)
		for key, val := range v {
			intVal, ok := val.(int64)
			if !ok {
				return fmt.Errorf("wait_after map values must be integers")
			}
			w.PerDep[key] = int(intVal)
		}
		w.IsPerDep = true
	default:
		return fmt.Errorf("wait_after must be an integer or a map of dependency names to wait times")
	}
	return nil
}

func (w *WaitAfterField) GetWaitTime(depName string) int {
	if w.IsPerDep {
		if waitTime, exists := w.PerDep[depName]; exists {
			return waitTime
		}
		return 0
	}
	return w.Global
}

type HealthCheckConfig struct {
	Endpoint   string `toml:"endpoint,omitempty"`
	Command    string `toml:"command,omitempty"`
	Interval   int    `toml:"interval,omitempty"`
	Retries    int    `toml:"retries,omitempty"`
	Timeout    int    `toml:"timeout,omitempty"`
	StartDelay int    `toml:"start_delay,omitempty"`
}

type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

type Service struct {
	Name         string             `toml:"name"`
	Command      string             `toml:"command"`
	LogFile      string             `toml:"log_file,omitempty"`
	PreScript    string             `toml:"pre_script,omitempty"`
	PosScript    string             `toml:"pos_script,omitempty"`
	User         string             `toml:"user,omitempty"`
	Args         []string           `toml:"args"`
	DependsOn    DependsOnField     `toml:"depends_on,omitempty"`
	WaitAfter    *WaitAfterField    `toml:"wait_after,omitempty"`
	Enabled      *bool              `toml:"enabled,omitempty"`
	Required     bool               `toml:"required,omitempty"`
	Oneshot      bool               `toml:"oneshot,omitempty"`
	HealthCheck  *HealthCheckConfig `toml:"health_check,omitempty"`
	Restart      RestartPolicy      `toml:"restart,omitempty"`
	RestartDelay int                `toml:"restart_delay,omitempty"`
	MaxRestarts  int                `toml:"max_restarts,omitempty"`
	Env          map[string]string  `toml:"env,omitempty"`
	EnvFile      string             `toml:"env_file,omitempty"`
}

type Config struct {
	Services []Service `toml:"services"`
	Timeouts Timeouts  `toml:"timeouts,omitempty"`
}

type serviceRaw struct {
	Name         string             `toml:"name"`
	Command      string             `toml:"command"`
	LogFile      string             `toml:"log_file,omitempty"`
	PreScript    string             `toml:"pre_script,omitempty"`
	PosScript    string             `toml:"pos_script,omitempty"`
	User         string             `toml:"user,omitempty"`
	Args         []string           `toml:"args"`
	DependsOn    interface{}        `toml:"depends_on,omitempty"`
	WaitAfter    interface{}        `toml:"wait_after,omitempty"`
	Enabled      *bool              `toml:"enabled,omitempty"`
	Required     bool               `toml:"required,omitempty"`
	Oneshot      bool               `toml:"oneshot,omitempty"`
	HealthCheck  *HealthCheckConfig `toml:"health_check,omitempty"`
	Restart      string             `toml:"restart,omitempty"`
	RestartDelay int                `toml:"restart_delay,omitempty"`
	MaxRestarts  int                `toml:"max_restarts,omitempty"`
	Env          map[string]string  `toml:"env,omitempty"`
	EnvFile      string             `toml:"env_file,omitempty"`
}

type configRaw struct {
	Services []serviceRaw `toml:"services"`
	Timeouts Timeouts     `toml:"timeouts,omitempty"`
}

func parseConfig(r io.Reader) (Config, error) {
	var raw configRaw
	if err := toml.NewDecoder(r).Decode(&raw); err != nil {
		return Config{}, err
	}

	cfg := Config{Timeouts: raw.Timeouts}
	for i := range raw.Services {
		sr := &raw.Services[i]
		if sr.Name == "" {
			continue
		}
		var wa *WaitAfterField
		switch v := sr.WaitAfter.(type) {
		case nil:
		case int64:
			wa = &WaitAfterField{Global: int(v), IsPerDep: false}
		case map[string]interface{}:
			mp := make(map[string]int)
			for k, anyVal := range v {
				iv, ok := anyVal.(int64)
				if !ok {
					return Config{}, fmt.Errorf("wait_after map values must be integers")
				}
				mp[k] = int(iv)
			}
			wa = &WaitAfterField{PerDep: mp, IsPerDep: true}
		default:
			return Config{}, fmt.Errorf("wait_after must be an integer or a map of dependency names to wait times")
		}

		var deps DependsOnField
		switch dv := sr.DependsOn.(type) {
		case nil:
		case string:
			deps = []string{dv}
		case []interface{}:
			out := make([]string, len(dv))
			for i, item := range dv {
				s, ok := item.(string)
				if !ok {
					return Config{}, fmt.Errorf("depends_on array must contain only strings")
				}
				out[i] = s
			}
			deps = out
		default:
			return Config{}, fmt.Errorf("depends_on must be a string or array of strings")
		}

		svc := Service{
			Name:         sr.Name,
			Command:      sr.Command,
			Args:         sr.Args,
			LogFile:      sr.LogFile,
			PreScript:    sr.PreScript,
			PosScript:    sr.PosScript,
			DependsOn:    deps,
			WaitAfter:    wa,
			Enabled:      sr.Enabled,
			User:         sr.User,
			Required:     sr.Required,
			Oneshot:      sr.Oneshot,
			HealthCheck:  sr.HealthCheck,
			Restart:      RestartPolicy(sr.Restart),
			RestartDelay: sr.RestartDelay,
			MaxRestarts:  sr.MaxRestarts,
			Env:          sr.Env,
			EnvFile:      sr.EnvFile,
		}
		cfg.Services = append(cfg.Services, svc)
	}
	return cfg, nil
}
