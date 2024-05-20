package appjson

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dokku/dokku/plugins/common"
)

// TriggerAppJSONProcessDeployParallelism returns the max number of processes to deploy in parallel
func TriggerAppJSONProcessDeployParallelism(appName string, processType string) error {
	appJSON, err := GetAppJSON(appName)
	if err != nil {
		return err
	}

	parallelism := 1
	for procType, formation := range appJSON.Formation {
		if procType != processType {
			continue
		}

		if formation.MaxParallel == nil {
			continue
		}

		if *formation.MaxParallel > 0 {
			parallelism = *formation.MaxParallel
		}
	}

	fmt.Println(parallelism)
	return nil
}

// TriggerAppJSONGetContent outputs the contents of the app-json file, if any
func TriggerAppJSONGetContent(appName string) error {
	if !common.HasAppJSON(appName) {
		fmt.Print("{}")
		return nil
	}

	b, err := os.ReadFile(common.GetProcessSpecificAppJSONPath(appName))
	if err != nil {
		return fmt.Errorf("Cannot read app.json file: %v", err)
	}

	content := strings.TrimSpace(string(b))
	if content == "" {
		fmt.Print("{}")
		return nil
	}

	fmt.Print(content)
	return nil
}

// TriggerCorePostDeploy sets a property to
// allow the app to be restored on boot
func TriggerCorePostDeploy(appName string) error {
	existingAppJSON := common.GetAppJSONPath(appName)
	processSpecificAppJSON := fmt.Sprintf("%s.%s", existingAppJSON, os.Getenv("DOKKU_PID"))
	if common.FileExists(processSpecificAppJSON) {
		if err := os.Rename(processSpecificAppJSON, existingAppJSON); err != nil {
			return err
		}
	} else if common.FileExists(fmt.Sprintf("%s.missing", processSpecificAppJSON)) {
		if err := os.Remove(fmt.Sprintf("%s.missing", processSpecificAppJSON)); err != nil {
			return err
		}

		if common.FileExists(existingAppJSON) {
			if err := os.Remove(existingAppJSON); err != nil {
				return err
			}
		}
	}

	return nil
}

// TriggerCorePostExtract ensures that the main app.json is the one specified by app-json-path
func TriggerCorePostExtract(appName string, sourceWorkDir string) error {
	appJSONPath := strings.Trim(reportComputedAppjsonpath(appName), "/")
	if appJSONPath == "" {
		appJSONPath = "app.json"
	}

	existingAppJSON := common.GetAppJSONPath(appName)
	files, err := filepath.Glob(fmt.Sprintf("%s.*", existingAppJSON))
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			return err
		}
	}

	processSpecificAppJSON := fmt.Sprintf("%s.%s", existingAppJSON, os.Getenv("DOKKU_PID"))
	results, _ := common.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger: "git-get-property",
		Args:    []string{appName, "source-image"},
	})
	appSourceImage := results.StdoutContents()

	results, _ = common.CallPlugnTrigger(common.PlugnTriggerInput{
		Trigger: "builder-get-property",
		Args:    []string{appName, "build-dir"},
	})
	buildDir := results.StdoutContents()

	repoDefaultAppJSONPath := path.Join(sourceWorkDir, "app.json")
	if appSourceImage == "" {
		repoAppJSONPath := path.Join(sourceWorkDir, buildDir, appJSONPath)
		if !common.FileExists(repoAppJSONPath) {
			if appJSONPath != "app.json" && common.FileExists(repoDefaultAppJSONPath) {
				if err := os.Remove(repoDefaultAppJSONPath); err != nil {
					return err
				}
			}
			return common.TouchFile(fmt.Sprintf("%s.missing", processSpecificAppJSON))
		}

		if err := common.Copy(repoAppJSONPath, processSpecificAppJSON); err != nil {
			return fmt.Errorf("Unable to extract app.json: %v", err.Error())
		}

		if appJSONPath != "app.json" {
			if err := common.Copy(repoAppJSONPath, repoDefaultAppJSONPath); err != nil {
				return fmt.Errorf("Unable to move app.json into place: %v", err.Error())
			}
		}
	} else {
		if err := common.CopyFromImage(appName, appSourceImage, path.Join(buildDir, appJSONPath), processSpecificAppJSON); err != nil {
			return common.TouchFile(fmt.Sprintf("%s.missing", processSpecificAppJSON))
		}
	}

	// TODO: add validation to app.json file by ensuring it can be deserialized
	return nil
}

// TriggerInstall initializes app-json directory structures
func TriggerInstall() error {
	if err := common.PropertySetup("app-json"); err != nil {
		return fmt.Errorf("Unable to install the app-json plugin: %s", err.Error())
	}

	if err := common.SetupAppData("app-json"); err != nil {
		return err
	}

	return nil
}

// TriggerPostAppCloneSetup creates new app-json files
func TriggerPostAppCloneSetup(oldAppName string, newAppName string) error {
	err := common.PropertyClone("app-json", oldAppName, newAppName)
	if err != nil {
		return err
	}

	return common.CloneAppData("app-json", oldAppName, newAppName)
}

// TriggerPostAppRename removes the old app data
func TriggerPostAppRename(oldAppName string, newAppName string) error {
	return common.MigrateAppDataDirectory("app-json", oldAppName, newAppName)
}

// TriggerPostAppRenameSetup renames app-json files
func TriggerPostAppRenameSetup(oldAppName string, newAppName string) error {
	if err := common.PropertyClone("app-json", oldAppName, newAppName); err != nil {
		return err
	}

	if err := common.PropertyDestroy("app-json", oldAppName); err != nil {
		return err
	}

	return common.CloneAppData("app-json", oldAppName, newAppName)
}

// TriggerPostCreate ensures apps have the correct data directory structure
func TriggerPostCreate(appName string) error {
	return common.CreateAppDataDirectory("app-json", appName)
}

// TriggerPostDelete destroys the app-json data for a given app container
func TriggerPostDelete(appName string) error {
	dataErr := common.RemoveAppDataDirectory("app-json", appName)
	propertyErr := common.PropertyDestroy("app-json", appName)

	if dataErr != nil {
		return dataErr
	}

	return propertyErr
}

// TriggerPostDeploy is a trigger to execute the postdeploy deployment task
func TriggerPostDeploy(appName string, imageTag string) error {
	image, err := common.GetDeployingAppImageName(appName, imageTag, "")
	if err != nil {
		return err
	}

	return executeScript(appName, image, imageTag, "postdeploy")
}

func TriggerPreReleaseBuilder(builderType string, appName string, image string) error {
	parts := strings.Split(image, ":")
	imageTag := parts[len(parts)-1]
	return executeScript(appName, image, imageTag, "predeploy")
}

// TriggerPostReleaseBuilder is a trigger to execute predeploy and release deployment tasks
func TriggerPostReleaseBuilder(builderType string, appName string, image string) error {
	parts := strings.Split(image, ":")
	imageTag := parts[len(parts)-1]
	if err := executeScript(appName, image, imageTag, "release"); err != nil {
		return err
	}

	if err := setScale(appName, image); err != nil {
		return err
	}

	if common.PropertyGet("common", appName, "deployed") == "true" {
		return nil
	}

	// Ensure that a failed postdeploy does not trigger twice
	if common.PropertyGet("app-json", appName, "heroku.postdeploy") == "executed" {
		return nil
	}

	if err := common.PropertyWrite("app-json", appName, "heroku.postdeploy", "executed"); err != nil {
		return err
	}

	return executeScript(appName, image, imageTag, "heroku.postdeploy")
}
