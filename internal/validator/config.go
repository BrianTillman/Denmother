package validator

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/BrianTillman/Denmother/internal/project"

	"github.com/BrianTillman/Denmother/internal/util"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/fatih/color"
)

// validateConfig checks HA config structure
func validateConfig(configPath string) error {
	return validateConfigContext(context.Background(), configPath)
}

func validateConfigContext(ctx context.Context, configPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	color.New(color.FgBlue).Println("Checking configuration structure...")
	fmt.Println()

	var hasErrors bool

	configFile := filepath.Join(configPath, "configuration.yaml")
	if util.FileExists(configFile) {
		color.Green("  %s Found configuration.yaml", checkMark)
	} else {
		color.Red("  %s Missing configuration.yaml", crossMark)
		hasErrors = true
	}

	automationsDir := filepath.Join(configPath, "automations")
	automationsFile := filepath.Join(configPath, "automations.yaml")
	if util.DirExists(automationsDir) {
		files, _ := doublestar.FilepathGlob(filepath.Join(automationsDir, "**/*.yaml"))
		if len(files) > 0 {
			color.Green("  %s Found %d automation files", checkMark, len(files))
		} else {
			color.Yellow("  %s automations/ exists but contains no YAML files", warning)
		}
	} else if util.FileExists(automationsFile) {
		color.Green("  %s Found automations.yaml", checkMark)
	} else {
		settings, err := project.ForConfig(configPath)
		if err != nil {
			return err
		}
		if settings.ManagedRepository {
			color.Red("  %s Missing automations configuration", crossMark)
			hasErrors = true
		} else {
			color.Yellow("  %s No separate automations file or directory (inline/package configuration is allowed)", warning)
		}
	}

	scenesDir := filepath.Join(configPath, "scenes")
	scenesFile := filepath.Join(configPath, "scenes.yaml")
	if util.DirExists(scenesDir) {
		files, _ := doublestar.FilepathGlob(filepath.Join(scenesDir, "**/*.yaml"))
		if len(files) > 0 {
			color.Green("  %s Found %d scene files", checkMark, len(files))
		} else {
			color.Yellow("  %s scenes/ exists but contains no YAML files", warning)
		}
	} else if util.FileExists(scenesFile) {
		color.Green("  %s Found scenes.yaml", checkMark)
	} else {
		color.Yellow("  %s No scenes configuration found", warning)
	}

	blueprintsDir := filepath.Join(configPath, "blueprints")
	if util.DirExists(blueprintsDir) {
		files, _ := doublestar.FilepathGlob(filepath.Join(blueprintsDir, "**/*.yaml"))
		color.Cyan("  i Found %d blueprint files", len(files))
	}

	homekitDir := filepath.Join(configPath, "homekit")
	if util.DirExists(homekitDir) {
		files, _ := doublestar.FilepathGlob(filepath.Join(homekitDir, "**/*.yaml"))
		if len(files) > 0 {
			color.Green("  %s Found %d HomeKit bridge configs", checkMark, len(files))
		}
	}

	helpersDir := filepath.Join(configPath, "helpers")
	if util.DirExists(helpersDir) {
		files, _ := doublestar.FilepathGlob(filepath.Join(helpersDir, "**/*.yaml"))
		if len(files) > 0 {
			color.Green("  %s Found %d helper configs", checkMark, len(files))
		}
	}

	fmt.Println()

	if hasErrors {
		return fmt.Errorf("config validation failed")
	}
	return ctx.Err()
}
