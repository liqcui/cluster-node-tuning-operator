/*
This command is used to run the Cluster OpenShift Cluster Node Tuning Operator tests extension for OpenShift.
It registers the Cluster Node Tuning Operator tests with the OpenShift Tests Extension framework
and provides a command-line interface to execute them.
For further information, please refer to the documentation at:
https://github.com/openshift-eng/openshift-tests-extension/blob/main/cmd/example-tests/main.go
*/
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/spf13/cobra"

	// The import below is necessary to ensure that the NTO operator tests are registered with the extension.
	// _ "github.com/openshift/cluster-node-tuning-operator/test/e2e/basic"
	_ "github.com/openshift/cluster-node-tuning-operator/test/extended"
	// - "github.com/openshift/origin/test/extended/node_tuning"
)

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "cluster-node-tuning-operator")

	// Suite: conformance/parallel (fast, parallel-safe)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!(name.contains("[Serial]") || name.contains("[Slow]"))`,
		},
	})

	// Suite: conformance/serial (explicitly serial tests)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`name.contains("[Serial]")`,
		},
	})

	// Suite: optional/slow (long-running tests)
	ext.AddSuite(e.Suite{
		Name:    "openshift/cluster-node-tuning-operator/optional/slow",
		Parents: []string{"openshift/optional/slow"},
		Qualifiers: []string{
			`name.contains("[Slow]")`,
		},
	})

	// Suite: all (includes everything)
	ext.AddSuite(e.Suite{
		Name: "openshift/cluster-node-tuning-operator/all",
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Ensure [Disruptive] tests are also [Serial]
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		if strings.Contains(spec.Name, "[Disruptive]") && !strings.Contains(spec.Name, "[Serial]") {
			spec.Name = strings.ReplaceAll(
				spec.Name,
				"[Disruptive]",
				"[Serial][Disruptive]",
			)
		}
	})

	// Preserve original-name labels for renamed tests
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "original-name:") {
				parts := strings.SplitN(label, "original-name:", 2)
				if len(parts) > 1 {
					spec.OriginalName = parts[1]
				}
			}
		}
	})

	// Ignore obsolete tests
	ext.IgnoreObsoleteTests(
	// "[sig-openshift-apiserver] <test name here>",
	)

	// Initialize environment before running any tests
	specs.AddBeforeAll(func() {
		// do stuff
	})

	// Let's scan for tests with a platform label and create the rule for them such as [platform:vsphere]
	foundPlatforms := make(map[string]string)
	for _, test := range specs.Select(et.NameContains("[platform:")).Names() {
		re := regexp.MustCompile(`\[platform:[a-z]*]`)
		match := re.FindStringSubmatch(test)
		for _, platformDef := range match {
			if _, ok := foundPlatforms[platformDef]; !ok {
				platform := platformDef[strings.Index(platformDef, ":")+1 : len(platformDef)-1]
				foundPlatforms[platformDef] = platform
				specs.Select(et.NameContains(platformDef)).
					Include(et.PlatformEquals(platform))
			}
		}

	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "Cluster Node Tuning Operator Tests Extension",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
