// Package validator provides configuration validation for Home Assistant YAML files.
// It includes YAML syntax checking, entity reference validation, optional project
// integration policy, and schema validation via Docker.
package validator

import "context"

// Options configures validation behavior
type Options struct {
	Fix     bool            // Show fix suggestions
	Context context.Context // Cancels validation and native subprocesses.
}

// Validator checks YAML syntax, configuration structure, entity references,
// and project integration policy. Schema checks run Home Assistant's check_config.
type Validator struct {
	configPath string
	options    Options
}

// New creates a new Validator instance with the given config path and options.
func New(configPath string, opts Options) *Validator {
	return &Validator{
		configPath: configPath,
		options:    opts,
	}
}

// WithContext binds every check to the caller's cancellation/deadline.
func (v *Validator) WithContext(ctx context.Context) *Validator {
	v.options.Context = ctx
	return v
}

func (o Options) context() context.Context {
	if o.Context != nil {
		return o.Context
	}
	return context.Background()
}

// ValidateYAML checks YAML syntax of all config files
func (v *Validator) ValidateYAML() error {
	return validateYAML(v.configPath, v.options)
}

// ValidateConfig checks HA config structure
func (v *Validator) ValidateConfig() error {
	return validateConfigContext(v.options.context(), v.configPath)
}

// ValidateGuard checks for forbidden integrations
func (v *Validator) ValidateGuard() error {
	return validateGuardContext(v.options.context(), v.configPath)
}

// CheckEntities retains static findings and unresolved dynamic-reference counts.
func (v *Validator) CheckEntities() CheckResult { return checkEntities(v.configPath, v.options) }

// ValidateEntities checks entity references
func (v *Validator) ValidateEntities() error {
	return validateEntities(v.configPath, v.options)
}

// ValidateSchema runs Home Assistant's check_config via docker
func (v *Validator) ValidateSchema() error {
	return checkSchemaContext(v.options.context(), v.configPath).Err()
}
