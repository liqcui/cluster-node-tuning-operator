package nto

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

const (
	MachineAPINamespace = "openshift-machine-api"
	//MapiMachineset means the fullname of mapi machineset
	MapiMachineset = "machinesets.machine.openshift.io"
	//MapiMachine means the fullname of mapi machine
	MapiMachine = "machines.machine.openshift.io"
	//MapiMHC means the fullname of mapi machinehealthcheck
	MapiMHC = "machinehealthchecks.machine.openshift.io"
)

var (
	fixtureDirLock sync.Once
	fixtureDir     string
)

func fixturePath(elem ...string) string {
	if len(elem) == 0 {
		panic("must specify path")
	}

	// Determine the base directory for test fixtures
	// This should be the absolute path to test/extended/testdata
	fixtureDirLock.Do(func() {
		// Get the current working directory or package directory
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		// Find the project root by looking for go.mod
		projectRoot := cwd
		for {
			if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(projectRoot)
			if parent == projectRoot {
				// Reached filesystem root without finding go.mod
				panic("could not find project root (go.mod)")
			}
			projectRoot = parent
		}

		fixtureDir = filepath.Join(projectRoot, "test", "extended", "testdata")
	})

	// Build the path to the fixture
	var pathComponents []string
	switch {
	case len(elem) > 3 && elem[0] == ".." && elem[1] == ".." && elem[2] == "examples":
		pathComponents = append([]string{filepath.Dir(filepath.Dir(fixtureDir))}, elem[2:]...)
	case len(elem) > 3 && elem[0] == ".." && elem[1] == ".." && elem[2] == "install":
		pathComponents = append([]string{filepath.Dir(filepath.Dir(fixtureDir))}, elem[2:]...)
	case len(elem) > 3 && elem[0] == ".." && elem[1] == "integration":
		pathComponents = append([]string{filepath.Dir(fixtureDir), "test"}, elem[1:]...)
	case elem[0] == "testdata":
		pathComponents = append([]string{fixtureDir}, elem[1:]...)
	default:
		pathComponents = append([]string{fixtureDir}, elem...)
	}

	fullPath := filepath.Join(pathComponents...)

	// Verify the path exists
	if _, err := os.Stat(fullPath); err != nil {
		panic(fmt.Sprintf("Fixture path does not exist: %s (error: %v)", fullPath, err))
	}

	p, err := filepath.Abs(fullPath)
	if err != nil {
		panic(err)
	}
	return p
}

// isPodInstalled will return true if any pod is found in the given namespace, and false otherwise
func isNTOPodInstalled(oc *CLI, namespace string) bool {

	e2e.Logf("checking if pod is found in namespace %s...", namespace)

	ntoDeployment, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", "-n", namespace, "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	if len(ntoDeployment) == 0 {
		e2e.Logf("no deployment cluster-node-tuning-operator found in namespace %s :(", namespace)
		return false
	}
	e2e.Logf("deployment %v found in namespace %s!", ntoDeployment, namespace)
	return true
}

// getNTOPodName checks all pods in a given namespace and returns the first NTO pod name found
func getNTOPodName(oc *CLI, namespace string) (string, error) {

	podListStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", namespace, "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	podList := strings.Fields(podListStr)
	podListSize := len(podList)
	for i := 0; i < podListSize; i++ {
		if strings.Contains(podList[i], "cluster-node-tuning-operator") {
			return podList[i], nil
		}
	}
	return "", fmt.Errorf("NTO pod was not found in namespace %s", namespace)
}

// getTunedState returns a string representation of the spec.managementState of the specified tuned in a given namespace
func getTunedState(oc *CLI, namespace string, tunedName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", tunedName, "-n", namespace, "-o=jsonpath={.spec.managementState}").Output()
}

// patchTunedState will patch the state of the specified tuned to that specified if supported, will throw an error if patch fails or state unsupported
func patchTunedState(oc *CLI, namespace string, tunedName string, state string) error {

	state = strings.ToLower(state)
	if state == "unmanaged" {
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Unmanaged"}}`, "--type", "merge", "-n", namespace).Execute()
	} else if state == "managed" {
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Managed"}}`, "--type", "merge", "-n", namespace).Execute()
	} else if state == "removed" {
		return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "-p", `{"spec":{"managementState":"Removed"}}`, "--type", "merge", "-n", namespace).Execute()
	} else {
		return fmt.Errorf("specified state %s is unsupported", state)
	}
}

// getTunedPriority returns a string representation of the spec.recommend.priority of the specified tuned in a given namespace
func getTunedPriority(oc *CLI, namespace string, tunedName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", tunedName, "-n", namespace, "-o=jsonpath={.spec.recommend[*].priority}").Output()
}

// patchTunedPriority will patch the priority of the specified tuned to that specified in a given YAML or JSON file
// we cannot directly patch the value since it is nested within a list, thus the need for a patch file for this function
func patchTunedProfile(oc *CLI, namespace string, tunedName string, patchFile string) error {
	return oc.AsAdmin().WithoutNamespace().Run("patch").Args("tuned", tunedName, "--patch-file="+patchFile, "--type", "merge", "-n", namespace).Execute()
}

// getTunedProfile returns a string representation of the status.tunedProfile of the given node in the given namespace
func getTunedProfile(oc *CLI, namespace string, tunedNodeName string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", namespace, "-o=jsonpath={.status.tunedProfile}").Output()
}

// assertIfTunedProfileApplied checks the logs for a given tuned pod in a given namespace to see if the expected profile was applied
func assertIfTunedProfileApplied(oc *CLI, namespace string, tunedNodeName string, tunedName string) {

	o.Eventually(func() bool {
		appliedStatus, err1 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
		tunedProfile, err2 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, "-ojsonpath={.status.tunedProfile}").Output()
		if err1 != nil || err2 != nil || strings.Contains(appliedStatus, "False") || strings.Contains(appliedStatus, "Unknown") || tunedProfile != tunedName {
			e2e.Logf("failed to apply profile to nodes, the status is %s and profile is %s, check again", appliedStatus, tunedProfile)
		}
		return strings.Contains(appliedStatus, "True") && tunedProfile == tunedName
	}, 15*time.Second, time.Second).Should(o.BeTrue())
}

// assertIfNodeSchedulingDisabled checks all nodes in a cluster to see if 'SchedulingDisabled' status is present on any node
func assertIfNodeSchedulingDisabled(oc *CLI) string {

	var nodeNames []string
	var nodeNameList []string
	err := wait.Poll(30*time.Second, 3*time.Minute, func() (bool, error) {
		nodeCheck, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		isNodeSchedulingDisabled := strings.Contains(nodeCheck, "SchedulingDisabled")
		isNodeNotReady := strings.Contains(nodeCheck, "NotReady")
		if isNodeSchedulingDisabled || isNodeNotReady {
			e2e.Logf("'schedulingdisabled' or 'notready' status found!")
			if isNodeNotReady {
				e2e.Logf("'notready' status found!")
				nodeNameReg := regexp.MustCompile(".*NotReady.*")
				nodeNameList = nodeNameReg.FindAllString(nodeCheck, -1)
			} else if isNodeSchedulingDisabled {
				nodeNameReg := regexp.MustCompile(".*SchedulingDisabled.*")
				nodeNameList = nodeNameReg.FindAllString(nodeCheck, -1)
			} else {
				e2e.Logf("'schedulingdisabled' or 'notready' isn't found!")
			}

			nodeNamestr := nodeNameList[0]
			nodeNames = strings.Split(nodeNamestr, " ")
			e2e.Logf("node names is %v", nodeNames)
			return true, nil
		}
		e2e.Logf("'schedulingdisabled' status not found - retrying...")
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "No node was found with 'SchedulingDisabled' status within timeout limit (3 minutes)")
	e2e.Logf("node name is %v", nodeNames[0])
	return nodeNames[0]
}

// assertIfMasterNodeChangesApplied checks all nodes in a cluster with the master role to see if 'default_hugepagesz=2M' is present on every node in /proc/cmdline
func assertIfMasterNodeChangesApplied(oc *CLI, masterNodeName string) {

	err := wait.Poll(1*time.Minute, 5*time.Minute, func() (bool, error) {
		output, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("node/"+masterNodeName, "--", "cat", "/proc/cmdline").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		isMasterNodeChanged := strings.Contains(output, "default_hugepagesz=2M")
		if isMasterNodeChanged {
			e2e.Logf("node %v has expected changes:\n%v", masterNodeName, output)
			return true, nil
		}
		e2e.Logf("node %v does not have expected changes - retrying...", masterNodeName)
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "Node"+masterNodeName+"did not have expected changes within timeout limit")
}

// getMaxUserWatchesValue parses out the line determining max_user_watches in inotify.conf
func getMaxUserWatchesValue(inotify string) string {
	reLine := regexp.MustCompile(`fs.inotify.max_user_watches = \d+`)
	reValue := regexp.MustCompile(`\d+`)
	maxUserWatches := reLine.FindString(inotify)
	maxUserWatchesValue := reValue.FindString(maxUserWatches)
	return maxUserWatchesValue
}

// getMaxUserInstancesValue parses out the line determining max_user_instances in inotify.conf
func getMaxUserInstancesValue(inotify string) string {
	reLine := regexp.MustCompile(`fs.inotify.max_user_instances = \d+`)
	reValue := regexp.MustCompile(`\d+`)
	maxUserInstances := reLine.FindString(inotify)
	maxUserInstancesValue := reValue.FindString(maxUserInstances)
	return maxUserInstancesValue
}

// getKernelPidMaxValue parses out the line determining pid_max in the kernel
func getKernelPidMaxValue(kernel string) string {
	reLine := regexp.MustCompile(`kernel.pid_max = \d+`)
	reValue := regexp.MustCompile(`\d+`)
	pidMax := reLine.FindString(kernel)
	pidMaxValue := reValue.FindString(pidMax)
	return pidMaxValue
}

// compareSpecifiedValueByNameOnLabelNode Compare if the sysctl parameter is equal to specified value on labeled node
func compareSpecifiedValueByNameOnLabelNode(oc *CLI, labelNodeName, sysctlparm, specifiedvalue string) {
	compareSpecifiedValueByNameOnLabelNodewithRetry(oc, "openshift-cluster-node-tuning-operator", labelNodeName, sysctlparm, specifiedvalue)
}

// compareSpecifiedValueByNameOnLabelNodewithRetry compares sysctl value with retry
func compareSpecifiedValueByNameOnLabelNodewithRetry(oc *CLI, ntoNamespace, nodeName, sysctlparm, specifiedvalue string) {
	err := wait.Poll(15*time.Second, 180*time.Second, func() (bool, error) {
		sysctlOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("--quiet=true", "--to-namespace="+ntoNamespace, "node/"+nodeName, "--", "chroot", "/host", "sysctl", sysctlparm).Output()
		sysctlOutput = strings.ReplaceAll(sysctlOutput, "\n", "")

		e2e.Logf("the actual value is [ %v ] on %v", sysctlOutput, nodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		regexpstr, _ := regexp.Compile(sysctlparm + " = " + specifiedvalue)
		matchStr := regexpstr.FindString(sysctlOutput)
		e2e.Logf("the match value is [ %v ] on %v", matchStr, nodeName)

		isMatch := regexpstr.MatchString(sysctlOutput)
		if isMatch {
			return true, nil
		}
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The value of sysctl mismatch, please check")
}

// compareSysctlDifferentFromSpecifiedValueByName compare if the sysctl parameter is not equal to specified value on all the node
func compareSysctlDifferentFromSpecifiedValueByName(oc *CLI, sysctlparm, specifiedvalue string) {

	nodeListStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	nodeList := strings.Fields(nodeListStr)
	nodeListSize := len(nodeList)

	regexpstr, _ := regexp.Compile(sysctlparm + ".*")
	for i := 0; i < nodeListSize; i++ {
		stdOut, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-q", "node/"+nodeList[i], "--", "chroot", "/host", "sysctl", sysctlparm).Output()
		conntrackMax := regexpstr.FindString(stdOut)
		e2e.Logf("the value is %v on %v", conntrackMax, nodeList[i])
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stdOut).NotTo(o.ContainSubstring(sysctlparm + " = " + specifiedvalue))
	}

}

// compareSysctlValueOnSepcifiedNodeByName compare the sysctl parameter's value on specified node, it should different than other node
func compareSysctlValueOnSepcifiedNodeByName(oc *CLI, tunedNodeName, sysctlparm, defaultvalue, specifiedvalue string) {

	nodeListStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	nodeList := strings.Fields(nodeListStr)
	nodeListSize := len(nodeList)

	// tuned nodes should have value of 1048578, others should be 1048576
	regexpstr, _ := regexp.Compile(sysctlparm + ".*")
	for i := 0; i < nodeListSize; i++ {
		stdOut, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-q", "node/"+nodeList[i], "--", "chroot", "/host", "sysctl", sysctlparm).Output()
		actualSysctlKeyValue := regexpstr.FindString(stdOut)
		e2e.Logf("The actual value is %v on %v", actualSysctlKeyValue, nodeList[i])
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeList[i] != tunedNodeName && len(defaultvalue) == 0 {
			e2e.Logf("the expected value of %v shouldn't be %v on %v", sysctlparm, specifiedvalue, nodeList[i])
			o.Expect(stdOut).NotTo(o.ContainSubstring(sysctlparm + " = " + specifiedvalue))
		} else {
			e2e.Logf("the expected value of %v should be %v on %v", sysctlparm, specifiedvalue, nodeList[i])
			o.Expect(stdOut).To(o.ContainSubstring(sysctlparm + " = " + specifiedvalue))
		}
	}
}

// getTunedPodNamebyNodeName
func getTunedPodNamebyNodeName(oc *CLI, tunedNodeName, namespace string) string {

	podNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", namespace, "--field-selector=spec.nodeName="+tunedNodeName, "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	//Get Pod name based on node name, and filter tuned pod name when mulitple pod return on the same node
	regexpstr, err := regexp.Compile(`tuned-.*`)
	o.Expect(err).NotTo(o.HaveOccurred())

	tunedPodName := regexpstr.FindString(podNames)
	e2e.Logf("the tuned pod name is: %v", tunedPodName)
	return tunedPodName
}

type ntoResource struct {
	name         string
	namespace    string
	template     string
	sysctlparm   string
	sysctlvalue  string
	priority     int
	deferedValue string
	label        string
}

func (ntoRes *ntoResource) createTunedProfileIfNotExist(oc *CLI) {

	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", ntoRes.name, "-n", ntoRes.namespace).Output()
	if strings.Contains(output, "NotFound") || strings.Contains(output, "No resources") || err != nil {
		e2e.Logf("no tuned in project: %s, create one: %s", ntoRes.namespace, ntoRes.name)
		processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.template, "-p", "TUNED_NAME="+ntoRes.name, "-p", "SYSCTLPARM="+ntoRes.sysctlparm, "-p", "SYSCTLVALUE="+ntoRes.sysctlvalue, "-o", "yaml").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.namespace, "-f", "-").InputString(processedTemplate).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else {
		e2e.Logf("already exist %v in project: %s", ntoRes.name, ntoRes.namespace)
	}
}

func (ntoRes *ntoResource) createDebugTunedProfileIfNotExist(oc *CLI, isDebug bool) {

	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", ntoRes.name, "-n", ntoRes.namespace).Output()
	if strings.Contains(output, "NotFound") || strings.Contains(output, "No resources") || err != nil {
		e2e.Logf("no tuned in project: %s, create one: %s", ntoRes.namespace, ntoRes.name)
		processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.template, "-p", "TUNED_NAME="+ntoRes.name, "-p", "SYSCTLPARM="+ntoRes.sysctlparm, "-p", "SYSCTLVALUE="+ntoRes.sysctlvalue, "-p", "ISDEBUG="+strconv.FormatBool(isDebug), "-o", "yaml").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.namespace, "-f", "-").InputString(processedTemplate).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else {
		e2e.Logf("already exist %v in project: %s", ntoRes.name, ntoRes.namespace)
	}
}

func (ntoRes *ntoResource) createIRQSMPAffinityProfileIfNotExist(oc *CLI) {

	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", ntoRes.name, "-n", ntoRes.namespace).Output()
	if strings.Contains(output, "NotFound") || strings.Contains(output, "No resources") || err != nil {
		e2e.Logf("no tuned in project: %s, create one: %s", ntoRes.namespace, ntoRes.name)
		processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.template, "-p", "TUNED_NAME="+ntoRes.name, "-p", "SYSCTLPARM="+ntoRes.sysctlparm, "-p", "SYSCTLVALUE="+ntoRes.sysctlvalue, "-o", "yaml").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", ntoRes.namespace, "-f", "-").InputString(processedTemplate).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else {
		e2e.Logf("already exist %v in project: %s", ntoRes.name, ntoRes.namespace)
	}
}

func (ntoRes *ntoResource) delete(oc *CLI) {
	_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoRes.namespace, "tuned", ntoRes.name, "--ignore-not-found").Execute()
}

// assertIfTunedProfileApplied checks the logs for a given tuned pod in a given namespace to see if the expected profile was applied
func (ntoRes *ntoResource) assertIfTunedProfileApplied(oc *CLI, namespace string, tunedNodeName string, tunedName string, expectedAppliedStatus string) {
	o.Eventually(func() bool {
		appliedStatus, err1 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
		tunedProfile, err2 := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, "-ojsonpath={.status.tunedProfile}").Output()
		if err1 != nil || err2 != nil || !strings.Contains(appliedStatus, expectedAppliedStatus) || strings.Contains(appliedStatus, "Unknown") || tunedProfile != tunedName {
			e2e.Logf("failed to apply profile to nodes, the status is %s and profile is %s, check again", appliedStatus, tunedProfile)
		}
		return strings.Contains(appliedStatus, expectedAppliedStatus) && tunedProfile == tunedName
	}, 15*time.Second, time.Second).Should(o.BeTrue())
}

func (ntoRes *ntoResource) applyNTOTunedProfile(oc *CLI) {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.template, "-p", "TUNED_PROFILE="+ntoRes.name, "-p", "SYSCTL_NAME="+ntoRes.sysctlparm, "-p", "SYSCTL_VALUE="+ntoRes.sysctlvalue, "-p", "LABEL_NAME="+ntoRes.label, "-o", "yaml").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", ntoRes.namespace, "-f", "-").InputString(processedTemplate).Execute()
	o.Expect(err).NotTo(o.HaveOccurred())
}

func (ntoRes *ntoResource) applyNTOTunedProfileWithDeferredAnnotation(oc *CLI) {
	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args("--ignore-unknown-parameters=true", "-f", ntoRes.template, "-p", "TUNED_PROFILE="+ntoRes.name, "-p", "SYSCTL_NAME="+ntoRes.sysctlparm, "-p", "SYSCTL_VALUE="+ntoRes.sysctlvalue, "-p", "LABEL_NAME="+ntoRes.label, "-p", "DEFERRED_VALUE="+ntoRes.deferedValue, "-o", "yaml").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", ntoRes.namespace, "-f", "-").InputString(processedTemplate).Execute()
	o.Expect(err).NotTo(o.HaveOccurred())
}

// assertDebugSettings
func assertDebugSettings(oc *CLI, tunedNodeName string, ntoNamespace string, isDebug string) bool {

	nodeProfile, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace).Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	regDebugCheck, err := regexp.Compile(".*Debug:.*" + isDebug)
	o.Expect(err).NotTo(o.HaveOccurred())

	isMatch := regDebugCheck.MatchString(nodeProfile)
	loglines := regDebugCheck.FindAllString(nodeProfile, -1)
	e2e.Logf("the result is: %v", loglines[0])
	return isMatch
}

func getDefaultSMPAffinityBitMaskbyCPUCores(oc *CLI, workerNodeName string) string {
	//Get CPU number in specified worker nodes
	cpuCoresStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", workerNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(cpuCoresStdOut).NotTo(o.BeEmpty())

	cpuCores, err := strconv.Atoi(cpuCoresStdOut)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(cpuCoresStdOut).NotTo(o.BeEmpty())

	cpuHexMask := make([]byte, 0, 2)
	if cpuCores%4 != 0 {

		modCPUCoresby4 := int(cpuCores % 4)
		var cpuCoresMask int
		switch modCPUCoresby4 {
		case 3:
			cpuCoresMask = 7
		case 2:
			cpuCoresMask = 3
		case 1:
			cpuCoresMask = 1
		}
		cpuHexMask = append(cpuHexMask, byte(cpuCoresMask))
	}

	for i := 0; i < cpuCores/4; i++ {
		cpuHexMask = append(cpuHexMask, 15)
	}

	cpuHexMaskStr := fmt.Sprintf("%x", cpuHexMask)
	cpuHexMaskFmt := strings.ReplaceAll(cpuHexMaskStr, "0", "")
	e2e.Logf("There are %d cores on worker node %s, the hex mask is %s", cpuCores, workerNodeName, cpuHexMaskFmt)

	return cpuHexMaskFmt
}

func convertCPUBitMaskToByte(cpuHexMask string) []byte {

	cpuHexMaskChars := []rune(cpuHexMask)
	cpuBitsMask := make([]byte, 0)
	cpuNum := 0

	for i := 0; i < len(cpuHexMaskChars); i++ {

		switch cpuHexMaskChars[i] {
		case 'f':
			cpuBitsMask = append(cpuBitsMask, 15)
			cpuNum = cpuNum + 4
		case '7':
			cpuBitsMask = append(cpuBitsMask, 7)
			cpuNum = cpuNum + 3
		case '3':
			cpuBitsMask = append(cpuBitsMask, 3)
			cpuNum = cpuNum + 2
		case '1':
			cpuBitsMask = append(cpuBitsMask, 1)
			cpuNum = cpuNum + 1
		}

	}

	e2e.Logf("The total CPU number is %v\nThe CPU HexMask is:\n%s\nThe CPU BitsMask is:\n%b\n", cpuNum, cpuHexMask, cpuBitsMask)
	return cpuBitsMask
}

func convertIsolatedCPURange2CPUList(isolatedCPURange string) []byte {

	//Get a separated cpu number list
	cpuList := make([]byte, 0, 8)
	//From [1,2,4-5,12-17,24-28,30-32]
	//To   [1 2 4 5 12 13 14 15 16 17 24 25 26 27 28 30 31 32]

	cpuRangeList := strings.Split(isolatedCPURange, ",")

	for i := 0; i < len(cpuRangeList); i++ {
		//if CPU range is 12-17 which contain "-"

		if strings.Contains(cpuRangeList[i], "-") {

			//Ignore such senario when cpu setting as 45-,-46
			if strings.HasPrefix(cpuRangeList[i], "-") {
				continue
			}
			//startCPU is 12
			//endCPU is 17
			//the CPU range must be two numbers
			cpuRange := strings.Split(cpuRangeList[i], "-")
			endCPU, _ := strconv.Atoi(cpuRange[1])
			startCPU, _ := strconv.Atoi(cpuRange[0])
			for i := 0; i <= endCPU-startCPU; i++ {
				cpus := startCPU + i
				cpuList = append(cpuList, byte(cpus))
			}

		} else {
			cpus, _ := strconv.Atoi(cpuRangeList[i])

			//Ignore 1,2,<no number>
			if len(cpuRangeList[i]) != 0 {
				cpuList = append(cpuList, byte(cpus))
			}
		}
	}
	return cpuList
}

func assertIsolateCPUCoresAffectedBitMask(cpuBitsMask []byte, isolatedCPU []byte) string {

	//Isolated CPU Range, 0,1,3-4,11-16,23-27
	//          27 26 25 24 ---------------------------------3 2 1 0
	//          27%6=3
	//[1111     1111         1111 1111 1111 1111 1111         1111] cpuBitMask
	//[0000     1111         1000 0001 1111 1000 0001         1011] isolatedCPU
	//--------------------------------------------------------------
	//[1111     0000         0111 1110 0000 0111 1110         0100] affinityCPUMask
	//  0         1            2    3   4     5   6             7    cpuBitMaskGroupsIndex
	//            6            5    4   3     2   1             0    isolatedCPUIndex
	//     maxValueOfIsolatedCPUIndex
	var affinityCPUMask string
	totalCPUBitMaskGroups := len(cpuBitsMask)
	totalIsolatedCPUNum := len(isolatedCPU)

	e2e.Logf("The total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	e2e.Logf("The max CPU that isolated is : %v\n", int(isolatedCPU[totalIsolatedCPUNum-1]))

	//The max CPU number is 27, Index is 15
	maxValueOfIsolatedCPUIndex := int(isolatedCPU[totalIsolatedCPUNum-1]) / 4
	e2e.Logf("totalCPUGroupNum is: %v\nmaxCPUGroupIndex is: %v\n", totalCPUBitMaskGroups, maxValueOfIsolatedCPUIndex)
	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {

		isolatedCPUIndex := int(isolatedCPU[i]) / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex
		// 3 => 1000 2=>0100 1=>0010 0=>0000
		modIsolatedCPUby4 := int(isolatedCPU[i] % 4)
		var isolatedCPUMask int
		switch modIsolatedCPUby4 {
		case 3:
			isolatedCPUMask = 8
		case 2:
			isolatedCPUMask = 4
		case 1:
			isolatedCPUMask = 2
		case 0:
			isolatedCPUMask = 1
		}

		valueOfCPUBitsMaskOnIndex := int(cpuBitsMask[cpuBitsMaskIndex]) ^ isolatedCPUMask
		e2e.Logf("%04b ^ %04b = %04b\n", cpuBitsMask[cpuBitsMaskIndex], isolatedCPUMask, valueOfCPUBitsMaskOnIndex)
		cpuBitsMask[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)
	}
	cpuBitsMaskStr := fmt.Sprintf("%x", cpuBitsMask)
	affinityCPUMask = strings.ReplaceAll(cpuBitsMaskStr, "0", "")
	e2e.Logf("affinityCPUMask is: %s\n", affinityCPUMask)
	return affinityCPUMask
}

func assertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask []byte, isolatedCPU []byte, defaultIRQSMPAffinity string) bool {
	defaultIRQSMPAffinity = strings.ReplaceAll(defaultIRQSMPAffinity, "\n", "")

	//Isolated CPU Range, 0,1,3-4,11-16,23-27
	//          27 26 25 24 ---------------------------------3 2 1 0
	//          27%6=3
	//[1111     1111         1111 1111 1111 1111 1111         1111] cpuBitMask
	//[0000     1111         1000 0001 1111 1000 0001         1011] isolatedCPU
	//--------------------------------------------------------------
	//[0000     1111         1000 0001 1111 1000 0001         1011] affinityCPUMask
	//  0         1            2    3   4     5   6             7    cpuBitMaskGroupsIndex
	//            6            5    4   3     2   1             0    isolatedCPUIndex
	//     maxValueOfIsolatedCPUIndex

	var affinityCPUMask string
	var isMatch bool
	totalCPUBitMaskGroups := len(cpuBitsMask)
	totalIsolatedCPUNum := len(isolatedCPU)

	e2e.Logf("The total isolated CPUs is: %v\n", totalIsolatedCPUNum)
	e2e.Logf("The max CPU that isolated is : %v\n", int(isolatedCPU[totalIsolatedCPUNum-1]))
	isolatedCPUMaskGroup := make([]byte, totalCPUBitMaskGroups)

	e2e.Logf("The initial isolatedCPUMask is %04b\n", isolatedCPUMaskGroup)

	maxValueOfCPUBitMaskGroupsIndex := totalCPUBitMaskGroups - 1
	for i := totalIsolatedCPUNum - 1; i >= 0; i-- {

		isolatedCPUIndex := int(isolatedCPU[i]) / 4

		cpuBitsMaskIndex := maxValueOfCPUBitMaskGroupsIndex - isolatedCPUIndex

		// 3 => 1000 2=>0100 1=>0010 0=>0000
		modIsolatedCPUby4 := int(isolatedCPU[i] % 4)
		var isolatedCPUMask int
		switch modIsolatedCPUby4 {
		case 3:
			isolatedCPUMask = 8
		case 2:
			isolatedCPUMask = 4
		case 1:
			isolatedCPUMask = 2
		case 0:
			isolatedCPUMask = 1
		}

		e2e.Logf("%04b | %04b = %04b\n", isolatedCPUMaskGroup[cpuBitsMaskIndex], isolatedCPUMask, int(isolatedCPUMaskGroup[cpuBitsMaskIndex])|isolatedCPUMask)
		valueOfCPUBitsMaskOnIndex := int(isolatedCPUMaskGroup[cpuBitsMaskIndex]) | isolatedCPUMask
		isolatedCPUMaskGroup[cpuBitsMaskIndex] = byte(valueOfCPUBitsMaskOnIndex)

	}

	//Remove additional 0 in the isolatedCPUMaskGroup
	e2e.Logf("cpuBitsMask is: %04b\n", isolatedCPUMaskGroup)
	cpuBitsMaskStr := fmt.Sprintf("%x", isolatedCPUMaskGroup)
	cpuBitsMaskRune := []rune(cpuBitsMaskStr)
	bitsMaskChars := make([]byte, 0, 2)

	for i := 1; i < len(cpuBitsMaskRune); i = i + 2 {
		bitsMaskChars = append(bitsMaskChars, byte(cpuBitsMaskRune[i]))
	}
	affinityCPUMask = string(bitsMaskChars)

	//If defaultIRQSMPAffinity start with 0, ie, 00020, remove 000 and change to 20
	if strings.HasPrefix(defaultIRQSMPAffinity, "0") || strings.HasPrefix(affinityCPUMask, "0") {
		defaultIRQSMPAffinity = strings.TrimLeft(defaultIRQSMPAffinity, "0")
		affinityCPUMask = strings.TrimLeft(affinityCPUMask, "0")
	}

	e2e.Logf("affinityCPUMask is: -%s-, defaultIRQSMPAffinity is -%s-\n", affinityCPUMask, defaultIRQSMPAffinity)
	if affinityCPUMask == defaultIRQSMPAffinity {
		isMatch = true
	}
	return isMatch
}

// AssertTunedAppliedMC Check if customed tuned applied via MCP
func AssertTunedAppliedMC(oc *CLI, mcNamePrefix string, filter string) {

	mcNameList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mc", "--no-headers", "-oname").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	e2e.Logf("The name of mcName is: %v", mcNameList)

	mcNameReg, _ := regexp.Compile(".*" + mcNamePrefix)
	mcName := mcNameReg.FindAllString(mcNameList, -1)
	e2e.Logf("The expected names of mcName is: %v", mcName)

	mcOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(mcName[0], "-oyaml").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(mcOutput).To(o.ContainSubstring(filter))

	//Print machineconfig content by filter
	mccontent, _ := regexp.Compile(".*" + filter + ".*")
	contentLines := mccontent.FindAllString(mcOutput, -1)
	e2e.Logf("The result is: %v", contentLines[0])
	o.Expect(mcOutput).To(o.ContainSubstring(filter))
}

// AssertTunedAppliedToNode Check if customed tuned applied to a certain node
func AssertTunedAppliedToNode(oc *CLI, tunedNodeName string, filter string) bool {

	cmdLineOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-q", "node/"+tunedNodeName, "--", "cat", "/proc/cmdline").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	var isMatch bool
	if strings.Contains(cmdLineOutput, filter) {
		//Print machineconfig content by filter
		cmdLineReg, _ := regexp.Compile(".*" + filter + ".*")
		contentLines := cmdLineReg.FindAllString(cmdLineOutput, -1)
		e2e.Logf("The result is: %v", contentLines[0])
		isMatch = true
	} else {
		e2e.Logf("the result mismatch the filter: %v", filter)
		isMatch = false
	}
	return isMatch
}

// assertNTOPodLogsLastLines     s
func assertNTOPodLogsLastLines(oc *CLI, namespace string, ntoPod string, lineN string, timeDurationSec int, filter string) {

	err := wait.Poll(15*time.Second, time.Duration(timeDurationSec)*time.Second, func() (bool, error) {

		//Remove err assert for SNO, the OCP will can not access temporily when master node restart or certificate key removed
		ntoPodLogs, _ := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", namespace, ntoPod, "--tail="+lineN).Output()

		regNTOPodLogs, err := regexp.Compile(".*" + filter + ".*")
		o.Expect(err).NotTo(o.HaveOccurred())
		isMatch := regNTOPodLogs.MatchString(ntoPodLogs)
		if isMatch {
			loglines := regNTOPodLogs.FindAllString(ntoPodLogs, -1)
			e2e.Logf("the logs of nto pod %v is: \n%v", ntoPod, loglines[0])
			return true, nil
		}
		e2e.Logf("the keywords of nto pod isn't found, try next ...")
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The tuned pod's log doesn't contain the keywords, please check")
}

// getServiceENDPoint
func getServiceENDPoint(oc *CLI, namespace string) string {
	var endPointIP string
	ipFamilyPolicy, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "service/node-tuning-operator", "-ojsonpath={.spec.ipFamilyPolicy}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(ipFamilyPolicy).NotTo(o.BeEmpty())

	serviceOutput, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("-n", namespace, "service/node-tuning-operator").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(serviceOutput).NotTo(o.BeEmpty())

	endPointReg, _ := regexp.Compile(".*Endpoints:.*")
	endPointIPStr := endPointReg.FindString(serviceOutput)
	o.Expect(endPointIPStr).NotTo(o.BeEmpty())

	if ipFamilyPolicy == "SingleStack" {
		endPointStr := strings.ReplaceAll(endPointIPStr, "         ", ",")
		o.Expect(endPointStr).NotTo(o.BeEmpty())

		endPointStrArr := strings.Split(endPointStr, ",")
		o.Expect(endPointStrArr).NotTo(o.BeEmpty())

		endPointStrArrLen := len(endPointStrArr)
		endPointIP = endPointStrArr[endPointStrArrLen-1]

	} else {
		endPointIPStrNoSpace := strings.ReplaceAll(endPointIPStr, " ", "")
		o.Expect(endPointIPStrNoSpace).NotTo(o.BeEmpty())

		endPointIPArr := strings.Split(endPointIPStrNoSpace, ":")
		o.Expect(endPointIPArr).NotTo(o.BeEmpty())

		endPointIP = endPointIPArr[1] + ":" + endPointIPArr[2]
	}

	return endPointIP
}

// AssertNTOCertificateRotate used for check if NTO certificate rotate
func AssertNTOCertificateRotate(oc *CLI, ntoNamespace string, tunedNodeName string, encodeBase64OpenSSLOutputBefore string, encodeBase64OpenSSLExpireDateBefore string) {

	metricEndpoint := getServiceENDPoint(oc, ntoNamespace)
	err := wait.Poll(15*time.Second, 300*time.Second, func() (bool, error) {

		openSSLOutputAfter, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("--quiet=true", "node/"+tunedNodeName, "--", "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		openSSLExpireDateAfter, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("--quiet=true", "node/"+tunedNodeName, "--", "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null  | /bin/openssl x509 -noout -dates").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("The openSSL Expired Date information of NTO openSSL after rotate as below: \n%v", openSSLExpireDateAfter)

		encodeBase64OpenSSLOutputAfter := base64.StdEncoding.EncodeToString([]byte(openSSLOutputAfter))
		encodeBase64OpenSSLExpireDateAfter := base64.StdEncoding.EncodeToString([]byte(openSSLExpireDateAfter))

		if encodeBase64OpenSSLOutputBefore != encodeBase64OpenSSLOutputAfter && encodeBase64OpenSSLExpireDateBefore != encodeBase64OpenSSLExpireDateAfter {
			e2e.Logf("the certificate has been updated ...")
			return true, nil
		}
		e2e.Logf("The certificate isn't updated, try next round ...")
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The NTO certificate isn't rotate, please check")
}

// compareCertificateBetweenOpenSSLandTLSSecret
func compareCertificateBetweenOpenSSLandTLSSecret(oc *CLI, ntoNamespace string, tunedNodeName string) {

	metricEndpoint := getServiceENDPoint(oc, ntoNamespace)
	err := wait.Poll(15*time.Second, 180*time.Second, func() (bool, error) {

		//Extract certificate from openssl that nto operator service endpoint
		openSSLOutputAfter, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null | sed -ne '/-BEGIN CERTIFICATE-/,/-END CERTIFICATE-/p'").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		//Extract tls.crt from secret node-tuning-operator-tls
		encodeBase64tlsCertOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "secret", "node-tuning-operator-tls", `-ojsonpath='{ .data.tls\.crt }'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		tmpTLSCertOutput := strings.Trim(encodeBase64tlsCertOutput, "'")
		decodedBytes, err := base64.StdEncoding.DecodeString(tmpTLSCertOutput)
		o.Expect(err).NotTo(o.HaveOccurred())
		tlsCertOutput := string(decodedBytes)

		if strings.Contains(tlsCertOutput, openSSLOutputAfter) {
			e2e.Logf("The certificate is the same ...")
			return true, nil
		}
		e2e.Logf("The certificate is different, try next round ...")
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The certificate is different, please check")
}

// assertIFChannel
func assertIFChannelQueuesStatus(oc *CLI, namespace string, tunedNodeName string) bool {

	var isMatch bool
	findStr := fmt.Sprintf(`find /sys/class/net -type l -not -lname *virtual* -a -not -name enP* -printf %%f"\n"`)
	ifNameList, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("--quiet=true", "--to-namespace="+namespace, "node/"+tunedNodeName, "--", "bash", "-c", findStr).Output()
	e2e.Logf("Physical network list is: %v", ifNameList)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(ifNameList).NotTo(o.BeEmpty())

	//Remove double quotes
	ifNameStr := strings.ReplaceAll(ifNameList, "\"", "")
	o.Expect(ifNameStr).NotTo(o.BeEmpty())
	//Check all physical nic
	ifNames := strings.Split(ifNameStr, "\n")
	e2e.Logf("ifNames is: %v", ifNames)
	o.Expect(ifNames).NotTo(o.BeEmpty())

	for i := 0; i < len(ifNames); {
		if len(ifNames[i]) > 0 {
			ethToolsOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("--quiet=true", "--to-namespace="+namespace, "node/"+tunedNodeName, "--", "ethtool", "-l", ifNames[i]).Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(ethToolsOutput).NotTo(o.BeEmpty())
			e2e.Logf("ethtool -l %v:, \n%v", ifNames[i], ethToolsOutput)

			regChannel, err := regexp.Compile("Combined:.*1")
			o.Expect(err).NotTo(o.HaveOccurred())
			isMatch = regChannel.MatchString(ethToolsOutput)
			if isMatch {
				break
			}
		}
		i++
	}
	return isMatch
}

// skipDeployPAO
func skipDeployPAO(oc *CLI) bool {

	skipPAO := true
	clusterVersion, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("clusterversion/version", "-ojsonpath={.status.desired.version}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	e2e.Logf("Cluster Version: %v", clusterVersion)
	paoDeployOCPVersionList := []string{"4.6", "4.7", "4.8", "4.9", "4.10"}

	for _, v := range paoDeployOCPVersionList {
		if strings.Contains(clusterVersion, v) {
			skipPAO = false
			break
		}
	}
	return skipPAO
}

// assertIOTimeOutandMaxRetries
func assertIOTimeOutandMaxRetries(oc *CLI, ntoNamespace string) {

	nodeListStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	nodeList := strings.Fields(nodeListStr)
	nodeListSize := len(nodeList)

	for i := 0; i < nodeListSize; i++ {
		timeoutOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+nodeList[i], "--", "chroot", "/host", "cat", "/sys/module/nvme_core/parameters/io_timeout").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The value of io_timeout is : %v on node %v", timeoutOutput, nodeList[i])
		o.Expect(timeoutOutput).To(o.ContainSubstring("4294967295"))
	}
}

// confirmedTunedReady
func confirmedTunedReady(oc *CLI, ntoNamespace string, tunedName string, timeDurationSec int) {

	err := wait.Poll(10*time.Second, time.Duration(timeDurationSec)*time.Second, func() (bool, error) {

		tunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		if strings.Contains(tunedStatus, tunedName) {
			return true, nil
		}
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "tuned is not ready")
}

// switchThrottlectlOnOff
func switchThrottlectlOnOff(oc *CLI, ntoNamespace, tunedNodeName string, throttlectlState string, timeDurationSec int) {

	err := wait.Poll(10*time.Second, time.Duration(timeDurationSec)*time.Second, func() (bool, error) {

		err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "host", "/usr/bin/throttlectl", throttlectlState).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		schedRTRuntimeStatus, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "cat", "/proc/sys/kernel/sched_rt_runtime_us").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		//Sleep 10s each time and retry two times to improve sucessful rate of restarting stalld
		if strings.Contains(schedRTRuntimeStatus, "-1") {
			return true, nil
		}
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "throttlectl status isn't correct, retry")
}

// assertProcessCgroupSchedulerBlacklist checks if process CPU affinity matches cgroup scheduler blacklist status
// If inBlacklist is true, checks if process uses all CPUs (0-N pattern)
// If inBlacklist is false, checks if process avoids CPU 1 (0 or 0,2-N pattern)
func assertProcessCgroupSchedulerBlacklist(oc *CLI, tunedNodeName string, namespace string, processFilter string, nodeCPUCores int, inBlacklist bool) bool {
	pIDCpusAllowedList, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", namespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "grep ^Cpus_allowed_list /proc/`pgrep "+processFilter+"`/status").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	e2e.Logf("Actually Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", pIDCpusAllowedList)

	nodeCPUCores = nodeCPUCores - 1
	var isMatch bool

	if inBlacklist {
		// Process IN Cgroup Blacklist uses all CPUs: 0-N (N is CPU Cores - 1)
		e2e.Logf("Expected Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", "Cpus_allowed_list:	0-"+strconv.Itoa(nodeCPUCores))
		regPIDCpusAllowedList, err := regexp.Compile(".*0-" + strconv.Itoa(nodeCPUCores))
		o.Expect(err).NotTo(o.HaveOccurred())
		isMatch = regPIDCpusAllowedList.MatchString(pIDCpusAllowedList)
	} else {
		// Process NOT In Cgroup Blacklist avoids CPU 1:
		// CPU = 2: uses only CPU 0
		// CPU > 2: uses CPU 0,2-N
		e2e.Logf("Expected Process's Cpus_allowed_list in /proc/$PID/status on worker nodes is: \n%v", "Cpus_allowed_list:	0 or 0,2-"+strconv.Itoa(nodeCPUCores))
		regPIDCpusAllowedList0, err := regexp.Compile(`.*0$`)
		o.Expect(err).NotTo(o.HaveOccurred())
		regPIDCpusAllowedList1, err := regexp.Compile(`.*0,2-.*`)
		o.Expect(err).NotTo(o.HaveOccurred())

		isMatch0 := regPIDCpusAllowedList0.MatchString(pIDCpusAllowedList)
		isMatch1 := regPIDCpusAllowedList1.MatchString(pIDCpusAllowedList)
		isMatch = isMatch0 || isMatch1
	}

	e2e.Logf("Match cgroup Cpus_allowed_list for process %v is: %v (inBlacklist: %v)", processFilter, isMatch, inBlacklist)
	return isMatch
}

// assertProcessNOTInCgroupSchedulerBlacklist checks if process is NOT in cgroup scheduler blacklist
func assertProcessNOTInCgroupSchedulerBlacklist(oc *CLI, tunedNodeName string, namespace string, processFilter string, nodeCPUCores int) bool {
	return assertProcessCgroupSchedulerBlacklist(oc, tunedNodeName, namespace, processFilter, nodeCPUCores, false)
}

// assertProcessInCgroupSchedulerBlacklist checks if process IS in cgroup scheduler blacklist
func assertProcessInCgroupSchedulerBlacklist(oc *CLI, tunedNodeName string, namespace string, processFilter string, nodeCPUCores int) bool {
	return assertProcessCgroupSchedulerBlacklist(oc, tunedNodeName, namespace, processFilter, nodeCPUCores, true)
}

// getNTOOperatorPodName retrun NTO operator POD name
func getNTOOperatorPodName(oc *CLI, namespace string) string {
	podName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", namespace, "pods", "-lname=cluster-node-tuning-operator", "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(podName).NotTo(o.BeEmpty())
	return podName
}

// assertCONodeTuningStatusWithoutWARNWithRetry sometime the WARN messages disappear with delay, so need to retry checking
func assertCONodeTuningStatusWithoutWARNWithRetry(oc *CLI, timeDurationSec int, filter string) {

	err := wait.Poll(time.Duration(timeDurationSec/10)*time.Second, time.Duration(timeDurationSec)*time.Second, func() (bool, error) {

		coNodeTuningStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("co/node-tuning").Output()
		if err != nil {
			e2e.Logf("the status of co/node-tuning abnormal, please check")
			return false, err
		}

		regCONodeTuningStdOut, err := regexp.Compile(".*" + filter + ".*")
		if err != nil {
			e2e.Logf("Un-supported filter %v, please check", filter)
			return false, err
		}

		isMatch := regCONodeTuningStdOut.MatchString(coNodeTuningStdOut)
		if isMatch {
			loglines := regCONodeTuningStdOut.FindAllString(coNodeTuningStdOut, -1)
			e2e.Logf("The status of co/node-tuning is:%v \n%v\n", coNodeTuningStdOut, loglines[0])
			e2e.Logf("The keywords of co/node-tuning still found, try next ...")
			return false, nil
		}
		return true, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The checking of co/node-tuning met with unexpected error, please check")
}

// getValueOfSysctlByName parses out the line determining sysctl in the kernel
func getValueOfSysctlByName(oc *CLI, ntoNamespace, tunedNodeName, sysctlparm string) string {

	var sysctlValue string
	defaultValues, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "sysctl", sysctlparm).Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(defaultValues).NotTo(o.BeEmpty())
	sysctlArray := strings.Split(defaultValues, "=")
	sysctlLen := len(sysctlArray)
	if sysctlLen == 2 {
		sysctlValue = strings.TrimSpace(sysctlArray[1])
	}
	return sysctlValue
}

// assertNTOCustomProfileStatus return correct profile status
func assertNTOCustomProfileStatus(oc *CLI, ntoNamespace string, tunedNodeName string, expectedProfile string, expectedAppliedStatus string, expectedDegradedStatus string) bool {

	currentProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath={.status.tunedProfile}`).Output()
	currentProfile = strings.Trim(currentProfile, "'")
	e2e.Logf("currentProfile is %v", currentProfile)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(currentProfile).NotTo(o.BeEmpty())

	appliedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
	appliedStatus = strings.Trim(appliedStatus, "'")
	e2e.Logf("appliedStatus is %v", appliedStatus)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(appliedStatus).NotTo(o.BeEmpty())

	degradedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Degraded")].status}'`).Output()
	degradedStatus = strings.Trim(degradedStatus, "'")
	e2e.Logf("degradedStatus is %v", degradedStatus)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(degradedStatus).NotTo(o.BeEmpty())

	return appliedStatus == expectedAppliedStatus && degradedStatus == expectedDegradedStatus && currentProfile == expectedProfile
}

func isROSAHostedCluster(oc *CLI) bool {

	var clusterType string
	//Check if it's ROSA hosted cluster
	sharedDir := os.Getenv("SHARED_DIR")
	if len(sharedDir) != 0 {
		fmt.Println("SHARED_DIR was found ")
		byteArray, err := ioutil.ReadFile(sharedDir + "/cluster-type")
		if err != nil {
			clusterType = ""
		} else {
			clusterType = string(byteArray)
			clusterType = strings.ToLower(clusterType)
		}

	}
	return strings.Contains(clusterType, "rosa")
}

func getFirstMasterNodeName(oc *CLI) string {
	var firstMasterNodeName string
	//ibmcloud don't have {.items[*].status.addresses[?(@.type=="Hostname")].address}'
	masterNodeNamesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l node-role.kubernetes.io/control-plane=", `-oname`).Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(masterNodeNamesStr).NotTo(o.BeEmpty())
	masterNodeNamesArray := strings.Split(masterNodeNamesStr, "\n")

	if len(masterNodeNamesArray) > 0 {
		firstMasterNodeNameArr := strings.Split(masterNodeNamesArray[0], "/")
		if len(firstMasterNodeNameArr) > 1 {
			firstMasterNodeName = firstMasterNodeNameArr[1]
		}
	}
	return firstMasterNodeName
}

func getDefaultProfileNameOnMaster(oc *CLI, masterNodeName string) string {

	var defaultProfileName string

	defaultProfileName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", "openshift-cluster-node-tuning-operator", "profiles.tuned.openshift.io", masterNodeName, "-ojsonpath={.status.tunedProfile}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(defaultProfileName).NotTo(o.BeEmpty())

	e2e.Logf("defaultProfileName is %v on %v ", defaultProfileName, masterNodeName)
	return defaultProfileName
}

func assertCoStatusWithKeywords(oc *CLI, keywords string) {

	o.Eventually(func() bool {
		coStatus, err1 := oc.AsAdmin().WithoutNamespace().Run("get").Args("co").Output()
		if err1 != nil || !strings.Contains(coStatus, keywords) {
			e2e.Logf("failed to find the keywords, the status of co is %s , check again", coStatus)
		}
		return strings.Contains(coStatus, keywords)
	}, 60*time.Second, time.Second).Should(o.BeTrue())
}

// getLinuxWorkerMachinesets returns a list of Linux worker machinesets, filtering out Windows and edge nodes
func getLinuxWorkerMachinesets(oc *CLI) []string {
	machinesetList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", "openshift-machine-api", "machineset", "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())

	workerMachineSets := strings.Split(machinesetList, " ")
	e2e.Logf("workerMachineSets is %v", workerMachineSets)

	linuxMachineset := make([]string, 0, len(workerMachineSets))
	for _, machineset := range workerMachineSets {
		// Skip windows and edge nodes
		if strings.Contains(machineset, "windows") || strings.Contains(machineset, "edge") {
			e2e.Logf("skip windows or edge node [ %v ]", machineset)
		} else if len(machineset) > 0 {
			linuxMachineset = append(linuxMachineset, machineset)
		}
	}
	e2e.Logf("linuxMachineset is %v", linuxMachineset)
	return linuxMachineset
}

func getWorkerMachinesetName(oc *CLI, machineseetSN int) string {
	linuxMachineset := getLinuxWorkerMachinesets(oc)

	var machinesetName string
	if machineseetSN < len(linuxMachineset) {
		machinesetName = linuxMachineset[machineseetSN]
	}

	e2e.Logf("machinesetName is %v in getWorkerMachinesetName", machinesetName)
	return machinesetName
}

func choseOneWorkerNodeNotByMachineset(oc *CLI, choseBy int) string {
	//0 means the first worker node, 1 means the last worker node
	var tunedNodeName string
	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	workerNodes := strings.Fields(workerNodesStr)
	o.Expect(len(workerNodes)).To(o.BeNumerically(">", 0), "No worker nodes found")

	if choseBy == 0 {
		tunedNodeName = workerNodes[0]
		e2e.Logf("the tunedNodeName that we get inside choseOneWorkerNodeNotByMachineset when choseBy 0 is %v ", tunedNodeName)
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())
	} else if choseBy == 1 {
		tunedNodeName = workerNodes[len(workerNodes)-1]
		e2e.Logf("the tunedNodeName that we get inside choseOneWorkerNodeNotByMachineset when choseBy 1 is %v ", tunedNodeName)
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())
	} else {
		e2e.Logf("Invalid parameter for choseBy is %v ", choseBy)
	}
	return tunedNodeName
}

func choseOneWorkerNodeToRunCase(oc *CLI, choseBy int) string {
	//Prior to choose worker nodes with machineset
	var tunedNodeName string
	machinesetOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", "openshift-machine-api", "-o=jsonpath={.items[*].metadata.name}").Output()
	machineSetExists := (err == nil && len(machinesetOutput) > 0)

	if machineSetExists {
		machinesetName := getWorkerMachinesetName(oc, choseBy)
		e2e.Logf("machinesetName is %v in choseOneWorkerNodeToRunCase", machinesetName)

		if len(machinesetName) != 0 {
			machinesetReplicas, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", "openshift-machine-api", machinesetName, "-o=jsonpath={.spec.replicas}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			if !strings.Contains(machinesetReplicas, "0") {
				// First check if any nodes exist with this machineset label
				nodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "machine.openshift.io/cluster-api-machineset="+machinesetName, "-o=jsonpath={.items[*].metadata.name}").Output()
				o.Expect(err).NotTo(o.HaveOccurred())
				if len(strings.TrimSpace(nodeNames)) > 0 {
					// Get the first node name
					nodeList := strings.Fields(nodeNames)
					tunedNodeName = nodeList[0]
					o.Expect(tunedNodeName).NotTo(o.BeEmpty())
				} else {
					e2e.Logf("No nodes found for machineset %s, falling back to node selection without machineset", machinesetName)
					tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, choseBy)
				}
			} else {
				tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, choseBy)
			}
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, choseBy)
		}
	} else {
		tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, choseBy)
		e2e.Logf("the tunedNodeName that we get inside choseOneWorkerNodeToRunCase when choseBy %v is %v ", choseBy, tunedNodeName)
	}
	return tunedNodeName
}

func getTotalLinuxMachinesetNum(oc *CLI) int {
	linuxMachineset := getLinuxWorkerMachinesets(oc)
	machinesetNum := len(linuxMachineset)
	e2e.Logf("machinesetNum is %v in getTotalLinuxMachinesetNum", machinesetNum)
	return machinesetNum
}

// WaitForNodesReady check if all the nodes are Ready in a MachineSet, then check if node has uninitialized taint, because healthy node should not has uninitialized taint
func WaitForNodesReady(oc *CLI, machineSetName string) {
	machineNumber := GetMachineSetReplicas(oc, machineSetName)
	if machineNumber >= 1 {
		e2e.Logf("Wait nodes ready then check nodes haven't uninitialized taints...")
		err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, 180*time.Second, false, func(cxt context.Context) (bool, error) {
			defer g.GinkgoRecover()
			for _, nodeName := range GetNodeNamesFromMachineSet(oc, machineSetName) {
				readyStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.status.conditions[?(@.type==\"Ready\")].status}").Output()
				// If node NotFound，skip check this node
				if strings.Contains(readyStatus, "NotFound") {
					e2e.Logf("Node %s does not exist, skipping...", nodeName)
					continue
				}
				o.Expect(err).NotTo(o.HaveOccurred())
				e2e.Logf("node %s readyStatus: %s", nodeName, readyStatus)
				if readyStatus != "True" {
					return false, nil
				}
				taints, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.spec.taints}").Output()
				o.Expect(err).NotTo(o.HaveOccurred())
				if strings.Contains(taints, "uninitialized") {
					e2e.Logf("Node %s has uninitialized taint %s, retrying...", nodeName, taints)
					return false, nil
				}
			}
			e2e.Logf("All nodes are ready and haven't uninitialized taints ...")
			return true, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "some nodes are not ready in 3 minutes")
	}
}

// GetMachineSetReplicas get MachineSet replicas
func GetMachineSetReplicas(oc *CLI, machineSetName string) int {
	e2e.Logf("Getting MachineSets replicas ...")
	replicasVal, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachineset, machineSetName, "-o=jsonpath={.spec.replicas}", "-n", MachineAPINamespace).Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	replicas, _ := strconv.Atoi(replicasVal)
	return replicas
}

// GetNodeNamesFromMachineSet get all Nodes in a Machineset
func GetNodeNamesFromMachineSet(oc *CLI, machineSetName string) []string {
	e2e.Logf("Getting all Nodes in a Machineset ...")
	nodeNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-o=jsonpath={.items[*].status.nodeRef.name}", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-n", MachineAPINamespace).Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	if nodeNames == "" {
		return []string{}
	}
	return strings.Split(nodeNames, " ")
}

// waitForMachinesRunning check if all the machines are Running in a MachineSet
func waitForMachinesRunning(oc *CLI, machineNumber int, machineSetName string) {
	e2e.Logf("Waiting for the machines Running ...")
	if machineNumber >= 1 {
		// Wait 180 seconds first, as it uses total 1200 seconds in wait.poll, it may not be enough for some platform(s)
		time.Sleep(180 * time.Second)
	}
	pollErr := wait.Poll(60*time.Second, 1200*time.Second, func() (bool, error) {
		msg, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachineset, machineSetName, "-o=jsonpath={.status.readyReplicas}", "-n", MachineAPINamespace).Output()
		machinesRunning, _ := strconv.Atoi(msg)
		if machinesRunning != machineNumber {
			phase, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-n", "openshift-machine-api", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=jsonpath={.items[*].status.phase}").Output()
			if strings.Contains(phase, "Failed") {
				output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-n", "openshift-machine-api", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
				e2e.Logf("%v", output)
				if strings.Contains(output, "error launching instance: Instances in the pgcluster Placement Group") {
					e2e.Logf("%v", output)
					return false, fmt.Errorf("error launching instance in the pgcluster Placement Group")
				}
				return false, fmt.Errorf("Some machine go into Failed phase!")
			}
			if strings.Contains(phase, "Provisioning") {
				output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-n", "openshift-machine-api", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
				if strings.Contains(output, "InsufficientInstanceCapacity") || strings.Contains(output, "InsufficientCapacityOnOutpost") {
					e2e.Logf("%v", output)
					return false, fmt.Errorf("InsufficientInstanceCapacity")
				}
				if strings.Contains(output, "InsufficientResources") {
					e2e.Logf("%v", output)
					return false, fmt.Errorf("InsufficientResources")
				}
			}
			e2e.Logf("Expected %v  machine are not Running yet and waiting up to 1 minutes ...", machineNumber)
			return false, nil
		}
		e2e.Logf("Expected %v  machines are Running", machineNumber)
		return true, nil
	})
	if pollErr != nil {
		if pollErr.Error() == "InsufficientInstanceCapacity" {
			g.Skip("InsufficientInstanceCapacity, skip this test")
		}
		if pollErr.Error() == "InsufficientResources" {
			g.Skip("InsufficientResources, skip this test")
		}
		if pollErr.Error() == "error launching instance in the pgcluster Placement Group" {
			g.Skip("launching instance in the pgcluster Placement Group Zone is not suppoted, skip this test")
		}
		output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-n", "openshift-machine-api", "-l", "machine.openshift.io/cluster-api-machineset="+machineSetName, "-o=yaml").Output()
		e2e.Logf("%v", output)
		e2e.Failf("Expected %v  machines are not Running after waiting up to 20 minutes ...", machineNumber)
	}
	e2e.Logf("All machines are Running ...")
	//add WaitForNodesReady here because we found sometimes the machine get Running but the node is still NotReady, it will take a little longer to be Ready
	if machineNumber >= 1 {
		WaitForNodesReady(oc, machineSetName)
	}
}
