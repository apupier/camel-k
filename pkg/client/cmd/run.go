/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/apache/camel-k/pkg/util"

	"github.com/apache/camel-k/pkg/util/sync"
	"github.com/sirupsen/logrus"

	"io"

	"github.com/apache/camel-k/pkg/apis/camel/v1alpha1"
	"github.com/apache/camel-k/pkg/util/kubernetes"
	"github.com/apache/camel-k/pkg/util/log"
	"github.com/apache/camel-k/pkg/util/watch"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/spf13/cobra"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCmdRun(rootCmdOptions *RootCmdOptions) *cobra.Command {
	options := runCmdOptions{
		RootCmdOptions: rootCmdOptions,
	}

	cmd := cobra.Command{
		Use:   "run [file to run]",
		Short: "Run a integration on Kubernetes",
		Long:  `Deploys and execute a integration pod on Kubernetes.`,
		Args:  options.validateArgs,
		RunE:  options.run,
	}

	cmd.Flags().StringVarP(&options.Language, "language", "l", "", "Programming Language used to write the file")
	cmd.Flags().StringVar(&options.IntegrationName, "name", "", "The integration name")
	cmd.Flags().StringSliceVarP(&options.Dependencies, "dependency", "d", nil, "The integration dependency")
	cmd.Flags().BoolVarP(&options.Wait, "wait", "w", false, "Waits for the integration to be running")
	cmd.Flags().StringVarP(&options.IntegrationContext, "context", "x", "", "The contex used to run the integration")
	cmd.Flags().StringSliceVarP(&options.Properties, "property", "p", nil, "Add a camel property")
	cmd.Flags().StringSliceVar(&options.ConfigMaps, "configmap", nil, "Add a ConfigMap")
	cmd.Flags().StringSliceVar(&options.Secrets, "secret", nil, "Add a Secret")
	cmd.Flags().BoolVar(&options.Logs, "logs", false, "Print integration logs")
	cmd.Flags().BoolVar(&options.Sync, "sync", false, "Synchronize the local source file with the cluster, republishing at each change")
	cmd.Flags().BoolVar(&options.Dev, "dev", false, "Enable Dev mode (equivalent to \"-w --logs --sync\")")
	cmd.Flags().BoolVar(&options.DependenciesAutoDiscovery, "auto-discovery", true, "Automatically discover Camel modules by analyzing user code")

	// completion support
	configureKnownCompletions(&cmd)

	return &cmd
}

type runCmdOptions struct {
	*RootCmdOptions
	IntegrationContext        string
	Language                  string
	IntegrationName           string
	Dependencies              []string
	Properties                []string
	ConfigMaps                []string
	Secrets                   []string
	Wait                      bool
	Logs                      bool
	Sync                      bool
	Dev                       bool
	DependenciesAutoDiscovery bool
}

func (*runCmdOptions) validateArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return errors.New("accepts 1 arg, received " + strconv.Itoa(len(args)))
	}
	fileName := args[0]
	if _, err := os.Stat(fileName); err != nil && os.IsNotExist(err) {
		return errors.New("file " + fileName + " does not exist")
	} else if err != nil {
		return errors.New("error while accessing file " + fileName)
	}
	return nil
}

func (o *runCmdOptions) run(cmd *cobra.Command, args []string) error {
	integration, err := o.createIntegration(cmd, args)
	if err != nil {
		return err
	}
	if o.Sync || o.Dev {
		err = o.syncIntegration(args[0])
		if err != nil {
			return err
		}
	}
	if o.Wait || o.Dev {
		err = o.waitForIntegrationReady(integration)
		if err != nil {
			return err
		}
	}
	if o.Logs || o.Dev {
		err = o.printLogs(integration)
		if err != nil {
			return err
		}
	}

	if o.Sync && !o.Logs && !o.Dev {
		// Let's add a wait point, otherwise the script terminates
		<-o.Context.Done()
	}
	return nil
}

func (o *runCmdOptions) waitForIntegrationReady(integration *v1alpha1.Integration) error {
	// Block this goroutine until the integration is in a final status
	changes, err := watch.StateChanges(o.Context, integration)
	if err != nil {
		return err
	}

	var lastStatusSeen *v1alpha1.IntegrationStatus

watcher:
	for {
		select {
		case <-o.Context.Done():
			return nil
		case i, ok := <-changes:
			if !ok {
				break watcher
			}
			lastStatusSeen = &i.Status
			phase := string(i.Status.Phase)
			if phase != "" {
				fmt.Println("integration \""+integration.Name+"\" in phase", phase)
				// TODO when we add health checks, we should wait until they are passed
				if i.Status.Phase == v1alpha1.IntegrationPhaseRunning || i.Status.Phase == v1alpha1.IntegrationPhaseError {
					// TODO display some error info when available in the status
					break watcher
				}
			}
		}
	}

	// TODO we may not be able to reach this state, since the build will be done without sources (until we add health checks)
	if lastStatusSeen != nil && lastStatusSeen.Phase == v1alpha1.IntegrationPhaseError {
		return errors.New("integration deployment failed")
	}
	return nil
}

func (o *runCmdOptions) printLogs(integration *v1alpha1.Integration) error {
	scraper := log.NewSelectorScraper(integration.Namespace, "camel.apache.org/integration="+integration.Name)
	reader := scraper.Start(o.Context)
	for {
		str, err := reader.ReadString('\n')
		if err == io.EOF || o.Context.Err() != nil {
			break
		} else if err != nil {
			return err
		}
		fmt.Print(str)
	}
	return nil
}

func (o *runCmdOptions) syncIntegration(file string) error {
	changes, err := sync.File(o.Context, file)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-o.Context.Done():
				return
			case <-changes:
				_, err := o.updateIntegrationCode(file)
				if err != nil {
					logrus.Error("Unable to sync integration: ", err)
				}
			}
		}
	}()
	return nil
}

func (o *runCmdOptions) createIntegration(cmd *cobra.Command, args []string) (*v1alpha1.Integration, error) {
	return o.updateIntegrationCode(args[0])
}

func (o *runCmdOptions) updateIntegrationCode(filename string) (*v1alpha1.Integration, error) {
	code, err := o.loadCode(filename)
	if err != nil {
		return nil, err
	}

	namespace := o.Namespace

	name := ""
	if o.IntegrationName != "" {
		name = o.IntegrationName
		name = kubernetes.SanitizeName(name)
	} else {
		name = kubernetes.SanitizeName(filename)
		if name == "" {
			name = "integration"
		}
	}

	codeName := filename

	if idx := strings.LastIndexByte(filename, os.PathSeparator); idx > -1 {
		codeName = codeName[idx+1:]
	}

	integration := v1alpha1.Integration{
		TypeMeta: v1.TypeMeta{
			Kind:       v1alpha1.IntegrationKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.IntegrationSpec{
			Source: v1alpha1.SourceSpec{
				Name:     codeName,
				Content:  code,
				Language: v1alpha1.Language(o.Language),
			},
			Dependencies:              make([]string, 0, len(o.Dependencies)),
			DependenciesAutoDiscovery: &o.DependenciesAutoDiscovery,
			Context:                   o.IntegrationContext,
			Configuration:             make([]v1alpha1.ConfigurationSpec, 0),
		},
	}

	for _, item := range o.Dependencies {
		if strings.HasPrefix(item, "mvn:") {
			integration.Spec.Dependencies = append(integration.Spec.Dependencies, item)
		} else if strings.HasPrefix(item, "file:") {
			integration.Spec.Dependencies = append(integration.Spec.Dependencies, item)
		} else if strings.HasPrefix(item, "camel-") {
			integration.Spec.Dependencies = append(integration.Spec.Dependencies, "camel:"+strings.TrimPrefix(item, "camel-"))
		}
	}

	// special handling for groovy
	// TODO: we should define handlers for languages and/or file extensions
	if o.Language == "groovy" && !util.StringSliceExists(o.Dependencies, "camel:groovy") {
		integration.Spec.Dependencies = append(integration.Spec.Dependencies, "camel:groovy")
	}
	if o.Language == "" && strings.HasSuffix(filename, ".groovy") {
		integration.Spec.Dependencies = append(integration.Spec.Dependencies, "camel:groovy")
	}

	for _, item := range o.Properties {
		integration.Spec.Configuration = append(integration.Spec.Configuration, v1alpha1.ConfigurationSpec{
			Type:  "property",
			Value: item,
		})
	}
	for _, item := range o.ConfigMaps {
		integration.Spec.Configuration = append(integration.Spec.Configuration, v1alpha1.ConfigurationSpec{
			Type:  "configmap",
			Value: item,
		})
	}
	for _, item := range o.Secrets {
		integration.Spec.Configuration = append(integration.Spec.Configuration, v1alpha1.ConfigurationSpec{
			Type:  "secret",
			Value: item,
		})
	}

	existed := false
	err = sdk.Create(&integration)
	if err != nil && k8serrors.IsAlreadyExists(err) {
		existed = true
		clone := integration.DeepCopy()
		err = sdk.Get(clone)
		if err != nil {
			return nil, err
		}
		integration.ResourceVersion = clone.ResourceVersion
		err = sdk.Update(&integration)
	}

	if err != nil {
		return nil, err
	}

	if !existed {
		fmt.Printf("integration \"%s\" created\n", name)
	} else {
		fmt.Printf("integration \"%s\" updated\n", name)
	}
	return &integration, nil
}

func (*runCmdOptions) loadCode(fileName string) (string, error) {
	content, err := ioutil.ReadFile(fileName)
	if err != nil {
		return "", err
	}
	// TODO check encoding issues
	return string(content), err
}
