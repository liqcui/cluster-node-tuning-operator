package nto

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	o "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

const machineAPINamespace = "openshift-machine-api"

// Helper functions for NTO extended tests

func getFirstLinuxWorkerNode(oc *CLI) (string, error) {
	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	nodes := strings.Fields(strings.TrimSpace(workerNodesStr))
	if len(nodes) == 0 {
		return "", fmt.Errorf("no Linux worker nodes found")
	}
	return nodes[0], nil
}

func getLastLinuxWorkerNode(oc *CLI) (string, error) {
	workerNodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	workerNodes := strings.Fields(workerNodesStr)
	if len(workerNodes) == 0 {
		return "", nil
	}
	return workerNodes[len(workerNodes)-1], nil
}

func isSNOCluster(oc *CLI) bool {
	nodeCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "--no-headers").Output()
	if err != nil {
		return false
	}
	return len(strings.Split(strings.TrimSpace(nodeCount), "\n")) == 1
}

func isOneMasterWithNWorkerNodes(oc *CLI) bool {
	masterCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane", "--no-headers").Output()
	if err != nil {
		return false
	}
	return len(strings.Split(strings.TrimSpace(masterCount), "\n")) == 1
}

func is3MasterNoDedicatedWorkerNode(oc *CLI) bool {
	masterCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/control-plane", "--no-headers").Output()
	if err != nil {
		return false
	}
	masterNodes := strings.Split(strings.TrimSpace(masterCount), "\n")

	workerCount, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=", "--no-headers").Output()
	if err != nil {
		return false
	}
	workerNodes := strings.Split(strings.TrimSpace(workerCount), "\n")

	return len(masterNodes) == 3 && (len(workerNodes) == 0 || (len(workerNodes) == 1 && workerNodes[0] == ""))
}

func getPodName(oc *CLI, namespace, selector, nodeName string) (string, error) {
	var args []string
	if selector != "" {
		args = append(args, "pods", "-n", namespace, "-l", selector)
	} else {
		args = append(args, "pods", "-n", namespace)
	}

	if nodeName != "" {
		args = append(args, "--field-selector=spec.nodeName="+nodeName)
	}

	args = append(args, "-o=jsonpath={.items[*].metadata.name}")

	podNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	if err != nil {
		return "", err
	}
	pods := strings.Fields(strings.TrimSpace(podNames))
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found matching the criteria")
	}
	return pods[0], nil
}

func getPodNodeName(oc *CLI, namespace, podName string) (string, error) {
	nodeName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.spec.nodeName}").Output()
	return strings.TrimSpace(nodeName), err
}

func labelPod(oc *CLI, namespace, podName, label string) error {
	return oc.AsAdmin().WithoutNamespace().Run("label").Args("pod", podName, "-n", namespace, label, "--overwrite").Execute()
}

func assertPodToBeReady(oc *CLI, podName, namespace string) {
	o.Eventually(func() bool {
		ready, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.status.conditions[?(@.type=='Ready')].status}").Output()
		if err != nil {
			return false
		}
		return strings.TrimSpace(ready) == "True"
	}, "5m", "10s").Should(o.BeTrue(), "Pod "+podName+" should be ready")
}

func createNsResourceFromTemplate(oc *CLI, namespace string, args ...string) error {
	var templateFile string
	var params []string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-f" && i+1 < len(args):
			templateFile = args[i+1]
			i++
		case args[i] == "-p" && i+1 < len(args):
			params = append(params, "-p", args[i+1])
			i++
		case strings.Contains(args[i], "=") && !strings.HasPrefix(args[i], "-"):
			params = append(params, "-p", args[i])
		}
	}

	processArgs := []string{"--ignore-unknown-parameters=true", "-f", templateFile}
	processArgs = append(processArgs, params...)
	processArgs = append(processArgs, "-o", "yaml")

	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args(processArgs...).Output()
	if err != nil {
		return err
	}

	return oc.AsAdmin().WithoutNamespace().Run("create").Args("-n", namespace, "-f", "-").InputString(processedTemplate).Execute()
}

func applyNsResourceFromTemplate(oc *CLI, namespace string, args ...string) error {
	var templateFile string
	var params []string

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-f" && i+1 < len(args):
			templateFile = args[i+1]
			i++
		case args[i] == "-p" && i+1 < len(args):
			params = append(params, "-p", args[i+1])
			i++
		case strings.HasPrefix(args[i], "REAPPLY_SYSCTL="),
			strings.HasPrefix(args[i], "SYSCTLPARM="),
			strings.HasPrefix(args[i], "SYSCTLVALUE="),
			strings.HasPrefix(args[i], "TUNED_NAME="),
			strings.Contains(args[i], "=") && !strings.HasPrefix(args[i], "-"):
			params = append(params, "-p", args[i])
		}
	}

	processArgs := []string{"--ignore-unknown-parameters=true", "-f", templateFile}
	processArgs = append(processArgs, params...)
	processArgs = append(processArgs, "-o", "yaml")

	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args(processArgs...).Output()
	if err != nil {
		return err
	}

	return oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", namespace, "-f", "-").InputString(processedTemplate).Execute()
}

func debugNodeWithChroot(oc *CLI, nodeName string, command ...string) (string, error) {
	args := []string{"node/" + nodeName, "--", "chroot", "/host"}
	args = append(args, command...)
	return oc.AsAdmin().WithoutNamespace().Run("debug").Args(args...).Output()
}

func getAllNodesbyOSType(oc *CLI, osType string) ([]string, error) {
	nodesStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "kubernetes.io/os="+osType, "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(nodesStr), nil
}

func remoteShPod(oc *CLI, namespace, podName string, command ...string) (string, error) {
	args := []string{podName, "-n", namespace, "--"}
	args = append(args, command...)
	return oc.AsAdmin().WithoutNamespace().Run("exec").Args(args...).Output()
}

func assertIfMCPChangesAppliedByName(oc *CLI, mcpName string, timeDurationSec int) {
	// Sleep 30s first
	time.Sleep(30 * time.Second)

	err := wait.PollUntilContextTimeout(context.Background(), time.Duration(timeDurationSec/10)*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(cxt context.Context) (bool, error) {

		var (
			mcpMachineCount         string
			mcpReadyMachineCount    string
			mcpUpdatedMachineCount  string
			mcpDegradedMachineCount string
			mcpUpdatingStatus       string
			mcpUpdatedStatus        string
			err                     error
		)

		mcpUpdatingStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, `-ojsonpath='{.status.conditions[?(@.type=="Updating")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(mcpUpdatingStatus).NotTo(o.BeEmpty())

		mcpUpdatedStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, `-ojsonpath='{.status.conditions[?(@.type=="Updated")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(mcpUpdatedStatus).NotTo(o.BeEmpty())

		//Do not check master err due to sometimes SNO can not accesss api server when server rebooted
		mcpMachineCount, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={..status.machineCount}").Output()
		mcpReadyMachineCount, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={..status.readyMachineCount}").Output()
		mcpUpdatedMachineCount, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={..status.updatedMachineCount}").Output()
		mcpDegradedMachineCount, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("mcp", mcpName, "-o=jsonpath={..status.degradedMachineCount}").Output()

		if strings.Contains(mcpUpdatingStatus, "False") && strings.Contains(mcpUpdatedStatus, "True") && mcpMachineCount == mcpReadyMachineCount && mcpMachineCount == mcpUpdatedMachineCount && mcpDegradedMachineCount == "0" {
			e2e.Logf("machineconfigpool [%v] checks succeeded!", mcpName)
			return true, nil
		}

		e2e.Logf("machineconfigpool [%v] checks failed, the following values were found (all should be '%v'):\nmachinecount: %v\nmcpupdatingstatus: %v\nmcpupdatedstatus: %v\nreadymachinecount: %v\nupdatedmachinecount: %v\nmcpdegradedmachine:%v\nretrying...", mcpName, mcpMachineCount, mcpMachineCount, mcpUpdatingStatus, mcpUpdatedStatus, mcpReadyMachineCount, mcpUpdatedMachineCount, mcpDegradedMachineCount)
		return false, nil

	})

	o.Expect(err).NotTo(o.HaveOccurred(), "MachineConfigPool checks were not successful within timeout limit")
}

func debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc *CLI, nodeName string, options []string, command ...string) (string, string, error) {
	args := options
	args = append(args, "node/"+nodeName, "--", "chroot", "/host")
	args = append(args, command...)
	stdout, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args(args...).Output()
	return stdout, "", err // stderr is not easily separated, so return empty string
}

func debugNodeRetryWithOptionsAndChrootWithStdErr(oc *CLI, nodeName string, options []string, command ...string) (string, string, error) {
	args := options
	args = append(args, "node/"+nodeName, "--", "chroot", "/host")
	args = append(args, command...)
	stdout, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args(args...).Output()
	return stdout, "", err
}

func getClusterVersion(oc *CLI) (string, string, error) {
	version, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("clusterversion/version", "-ojsonpath={.status.desired.version}").Output()
	return strings.TrimSpace(version), "", err
}

func assertOprPodLogsbyFilter(oc *CLI, podName, namespace, filter string, expectedCount int) bool {
	logs, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args(podName, "-n", namespace).Output()
	if err != nil {
		return false
	}
	count := strings.Count(logs, filter)
	return count >= expectedCount
}

func assertOprPodLogsbyFilterWithDuration(oc *CLI, podName, namespace, filter string, timeoutSeconds, expectedCount int) {
	o.Eventually(func() bool {
		logs, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args(podName, "-n", namespace, "--tail=100").Output()
		if err != nil {
			return false
		}
		count := strings.Count(logs, filter)
		return count >= expectedCount
	}, timeoutSeconds, 5).Should(o.BeTrue(), "Expected log filter "+filter+" to appear at least "+string(rune(expectedCount))+" times")
}

func waitForNoPodsAvailableByKind(oc *CLI, kind, name, namespace string) {
	o.Eventually(func() bool {
		pods, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pods", "-n", namespace, "-l", "app="+name, "-o=jsonpath={.items[*].metadata.name}").Output()
		if err != nil {
			return false
		}
		return len(strings.TrimSpace(pods)) == 0
	}, "10m", "10s").Should(o.BeTrue(), "Pods for "+kind+" "+name+" should be deleted")
}

func deleteMCAndMCPByName(oc *CLI, mcName, mcpName string, timeoutSeconds int) {
	_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineconfig", mcName, "--ignore-not-found").Execute()
	_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineconfigpool", mcpName, "--ignore-not-found").Execute()

	o.Eventually(func() bool {
		mc, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineconfig", mcName).Output()
		mcp, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineconfigpool", mcpName).Output()
		e2e.Logf("oc get mc status is: %s oc get mcp status is: %s", mc, mcp)
		return len(mc) == 0 && len(mcp) == 0 || strings.Contains(mc, "NotFound") && strings.Contains(mcp, "NotFound")
	}, timeoutSeconds, 10).Should(o.BeTrue(), "MC and MCP should be deleted")
}

func applyClusterResourceFromTemplate(oc *CLI, args ...string) error {
	var templateFile string
	var params []string

	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) {
			templateFile = args[i+1]
			i++
		} else if args[i] == "-p" && i+1 < len(args) {
			params = append(params, "-p", args[i+1])
			i++
		}
	}

	processArgs := []string{"--ignore-unknown-parameters=true", "-f", templateFile}
	processArgs = append(processArgs, params...)
	processArgs = append(processArgs, "-o", "yaml")

	processedTemplate, err := oc.AsAdmin().WithoutNamespace().Run("process").Args(processArgs...).Output()
	if err != nil {
		return err
	}

	return oc.AsAdmin().WithoutNamespace().Run("apply").Args("-f", "-").InputString(processedTemplate).Execute()
}

func applyOperatorResourceByYaml(oc *CLI, namespace, yamlFile string) error {
	return oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", namespace, "-f", yamlFile).Execute()
}

func stringToBASE64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func remoteShPodWithBash(oc *CLI, namespace, podName, bashCommand string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("exec").Args(podName, "-n", namespace, "--", "/bin/bash", "-c", bashCommand).Output()
}

func debugNodeWithOptionsAndChroot(oc *CLI, nodeName string, options []string, command ...string) (string, error) {
	return DebugNodeWithOptionsAndChroot(oc, nodeName, options, command...)
}

func debugNodeRetryWithOptionsAndChroot(oc *CLI, nodeName string, options []string, command ...string) (string, error) {
	return DebugNodeWithOptionsAndChroot(oc, nodeName, options, command...)
}

func isMachineSetExist(oc *CLI) bool {
	machinesets, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", "openshift-machine-api", "-o=jsonpath={.items[*].metadata.name}").Output()
	return err == nil && len(strings.TrimSpace(machinesets)) > 0
}

func getImagestreamImageName(oc *CLI, imageStreamName string) string {
	imageName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("imagestream", imageStreamName, "-n", "openshift", "-o=jsonpath={.status.tags[0].items[0].dockerImageReference}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(imageName)
}

func implStringArrayContains(arr []string, str string) bool {
	for _, v := range arr {
		if v == str {
			return true
		}
	}
	return false
}

// GetNodeNameByMachineset used for get node name by machineset name
func GetNodeNameByMachineset(oc *CLI, machinesetName string) string {

	var machineName string
	machinesetLabels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachineset, machinesetName, "-n", "openshift-machine-api", "-ojsonpath={.spec.selector.matchLabels.machine\\.openshift\\.io/cluster-api-machineset}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(machinesetLabels).NotTo(o.BeEmpty())

	machineNameStr, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(MapiMachine, "-l", "machine.openshift.io/cluster-api-machineset="+machinesetLabels, "-n", "openshift-machine-api", "-oname").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(machineNameStr).NotTo(o.BeEmpty())

	machineNames := strings.Split(machineNameStr, "\n")
	if len(machineNames) > 0 {
		machineName = machineNames[0]
	}

	e2e.Logf("machineName is %v in GetNodeNameByMachineset", machineName)

	nodeName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(machineName, "-n", "openshift-machine-api", "-ojsonpath={.status.nodeRef.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(nodeName).NotTo(o.BeEmpty())
	return nodeName
}

func getNodeNameByMachineset(oc *CLI, machinesetName string) string {
	return GetNodeNameByMachineset(oc, machinesetName)
}

func countLinuxWorkerNodeNumByOS(oc *CLI) int {
	nodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return 0
	}
	nodeList := strings.Fields(strings.TrimSpace(nodes))
	return len(nodeList)
}

func getNodeListByLabel(oc *CLI, label string) []string {
	nodes, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", label, "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return []string{}
	}
	return strings.Fields(strings.TrimSpace(nodes))
}

func showSystemctlPropertyValueOfServiceUnitByName(oc *CLI, nodeName, namespace, serviceName, propertyName string) string {
	value, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", namespace, "--quiet=true", "node/"+nodeName, "--", "chroot", "/host", "systemctl", "show", serviceName, "--property="+propertyName).Output()
	if err != nil {
		return ""
	}
	parts := strings.SplitN(value, "=", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func getSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(timestampStr string) int64 {
	timestamp, err := strconv.ParseInt(strings.TrimSpace(timestampStr), 10, 64)
	if err != nil {
		return 0
	}
	return timestamp
}

func specifyMachinesetWithDifferentInstanceType(oc *CLI) string {
	platform := strings.ToLower(strings.TrimSpace(checkPlatform(oc)))
	if platform == "" {
		e2e.Logf("unable to determine platform for machineset instance selection")
		return ""
	}

	currentInstanceType, err := getMachineSetInstanceType(oc, platform)
	if err != nil {
		e2e.Logf("failed to get current machineset instance type: %v", err)
		return ""
	}
	currentInstanceType = strings.TrimSpace(currentInstanceType)
	o.Expect(currentInstanceType).NotTo(o.BeEmpty(), "current machineset instance type must not be empty")

	var expectedInstanceType string
	switch platform {
	case "aws":
		expectedInstanceType = swapInstanceType(currentInstanceType, "2xlarge", "xlarge", "m6i.xlarge")
	case "azure":
		expectedInstanceType = swapInstanceType(currentInstanceType, "DS3_v2", "DS2_v2", "Standard_DS2_v2")
	case "gcp":
		expectedInstanceType = swapInstanceType(currentInstanceType, "standard-4", "standard-2", "n1-standard-2")
	case "ibmcloud":
		expectedInstanceType = swapInstanceType(currentInstanceType, "4x16", "2x8", "bx2d-2x8")
	case "alibabacloud":
		expectedInstanceType = swapInstanceType(currentInstanceType, "sxlarge", "large", "ecs.g6.large")
	default:
		e2e.Logf("unsupported cloud provider %s for machineset selection", platform)
	}

	e2e.Logf("currentInstanceType is %v, expectedInstanceType is %v", currentInstanceType, expectedInstanceType)
	return expectedInstanceType
}

func createMachinesetbyInstanceType(oc *CLI, machinesetName, instanceType string) error {

	machinesetList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", machineAPINamespace, "-oname").Output()
	if err != nil {
		return fmt.Errorf("failed to list machinesets: %w", err)
	}
	o.Expect(strings.TrimSpace(machinesetList)).NotTo(o.BeEmpty(), "no machinesets available in the cluster")
	e2e.Logf("existing machinesets:\n%v", machinesetList)

	var sourceMachineset string
	for _, entry := range strings.Fields(strings.TrimSpace(machinesetList)) {
		nameParts := strings.Split(entry, "/")
		name := nameParts[len(nameParts)-1]
		if strings.Contains(name, "windows") || strings.Contains(name, "edge") {
			continue
		}
		if name != "" {
			sourceMachineset = name
			break
		}
	}
	if sourceMachineset == "" {
		return fmt.Errorf("no Linux machineset found to clone from")
	}

	e2e.Logf("using base machineset %s", sourceMachineset)
	machinesetYAML, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", sourceMachineset, "-n", machineAPINamespace, "-oyaml").Output()
	if err != nil {
		return fmt.Errorf("failed to fetch machineset %s: %w", sourceMachineset, err)
	}
	o.Expect(strings.TrimSpace(machinesetYAML)).NotTo(o.BeEmpty())

	replacedYAML := strings.ReplaceAll(machinesetYAML, sourceMachineset, machinesetName)

	platform, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.type}").Output()
	if err != nil {
		return fmt.Errorf("failed to determine platform: %w", err)
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	e2e.Logf("platform detected: %s", platform)

	var typeRegex *regexp.Regexp
	var replacement string
	switch platform {
	case "aws", "alibabacloud":
		typeRegex = regexp.MustCompile(`instanceType:\s*.*`)
		replacement = "instanceType: " + instanceType
	case "gcp":
		typeRegex = regexp.MustCompile(`machineType:\s*.*`)
		replacement = "machineType: " + instanceType
	case "azure":
		typeRegex = regexp.MustCompile(`vmSize:\s*.*`)
		replacement = "vmSize: " + instanceType
	case "ibmcloud":
		typeRegex = regexp.MustCompile(`profile:\s*.*`)
		replacement = "profile: " + instanceType
	default:
		return fmt.Errorf("unsupported platform %s for machineset updates", platform)
	}

	if !typeRegex.MatchString(replacedYAML) {
		return fmt.Errorf("unable to locate instance type field for platform %s", platform)
	}
	replacedYAML = typeRegex.ReplaceAllString(replacedYAML, replacement)

	replicaRegex := regexp.MustCompile(`replicas:\s*\d+`)
	if replicaRegex.MatchString(replacedYAML) {
		replacedYAML = replicaRegex.ReplaceAllString(replacedYAML, "replicas: 1")
	}

	outputDir := e2e.TestContext.OutputDir
	if outputDir == "" {
		outputDir = "."
	}
	tmpFile := filepath.Join(outputDir, fmt.Sprintf("%s-%s-new.yaml", oc.Namespace(), machinesetName))
	defer os.Remove(tmpFile)

	if err := os.WriteFile(tmpFile, []byte(replacedYAML), 0o600); err != nil {
		return fmt.Errorf("failed to write machineset manifest: %w", err)
	}

	if err := oc.AsAdmin().WithoutNamespace().Run("apply").Args("-n", machineAPINamespace, "-f", tmpFile).Execute(); err != nil {
		return fmt.Errorf("failed to apply machineset manifest: %w", err)
	}
	return nil
}

func getMachineSetInstanceType(oc *CLI, platform string) (string, error) {
	machinesetName, err := getFirstLinuxMachineset(oc)
	if err != nil {
		return "", err
	}

	var jsonPath string
	switch platform {
	case "aws", "alibabacloud":
		jsonPath = "{.spec.template.spec.providerSpec.value.instanceType}"
	case "gcp":
		jsonPath = "{.spec.template.spec.providerSpec.value.machineType}"
	case "azure":
		jsonPath = "{.spec.template.spec.providerSpec.value.vmSize}"
	case "ibmcloud":
		jsonPath = "{.spec.template.spec.providerSpec.value.profile}"
	default:
		return "", fmt.Errorf("unsupported platform %s for instance type lookup", platform)
	}

	value, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", machinesetName, "-n", machineAPINamespace, "-ojsonpath="+jsonPath).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func getFirstLinuxMachineset(oc *CLI) (string, error) {
	machinesetList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("machineset", "-n", machineAPINamespace, "-oname").Output()
	if err != nil {
		return "", fmt.Errorf("failed to list machinesets: %w", err)
	}

	for _, entry := range strings.Fields(strings.TrimSpace(machinesetList)) {
		nameParts := strings.Split(entry, "/")
		name := nameParts[len(nameParts)-1]
		if strings.Contains(name, "windows") || strings.Contains(name, "edge") || name == "" {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("no Linux machineset found")
}

func swapInstanceType(current, primarySuffix, alternateSuffix, fallback string) string {
	if strings.Contains(current, primarySuffix) {
		return strings.Replace(current, primarySuffix, alternateSuffix, 1)
	}
	if strings.Contains(current, alternateSuffix) {
		return strings.Replace(current, alternateSuffix, primarySuffix, 1)
	}
	return fallback
}

// StringsSliceElementsHasPrefix checks if any element in the slice has the given prefix
func StringsSliceElementsHasPrefix(slice []string, prefix string, caseSensitive bool) (bool, int) {
	for i, s := range slice {
		if caseSensitive {
			if strings.HasPrefix(s, prefix) {
				return true, i
			}
		} else {
			if strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix)) {
				return true, i
			}
		}
	}
	return false, -1
}

// IsNamespacePrivileged checks if a namespace has privileged security context constraints
func IsNamespacePrivileged(oc *CLI, namespace string) (bool, error) {
	labels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("namespace", namespace, "-o=jsonpath={.metadata.labels}").Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(labels, "pod-security.kubernetes.io/enforce:privileged") ||
		strings.Contains(labels, "security.openshift.io/scc.podSecurityLabelSync:false"), nil
}

// SetNamespacePrivileged sets privileged labels on a namespace
func SetNamespacePrivileged(oc *CLI, namespace string) error {
	err := oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce=privileged", "pod-security.kubernetes.io/audit=privileged", "pod-security.kubernetes.io/warn=privileged", "security.openshift.io/scc.podSecurityLabelSync=false", "--overwrite").Execute()
	return err
}

// RecoverNamespaceRestricted recovers namespace to restricted labels
func RecoverNamespaceRestricted(oc *CLI, namespace string) {
	_ = oc.AsAdmin().WithoutNamespace().Run("label").Args("namespace", namespace, "pod-security.kubernetes.io/enforce-", "pod-security.kubernetes.io/audit-", "pod-security.kubernetes.io/warn-", "security.openshift.io/scc.podSecurityLabelSync-").Execute()
}

// IsDefaultNodeSelectorEnabled checks if default node selector is enabled
func IsDefaultNodeSelectorEnabled(oc *CLI) bool {
	selector, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("scheduler", "cluster", "-o=jsonpath={.spec.defaultNodeSelector}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(selector) != ""
}

// IsWorkerNode checks if a node has the worker role
func IsWorkerNode(oc *CLI, nodeName string) bool {
	labels, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", nodeName, "-o=jsonpath={.metadata.labels}").Output()
	if err != nil {
		return false
	}
	return strings.Contains(labels, "node-role.kubernetes.io/worker")
}

// IsSpecifiedAnnotationKeyExist checks if a specific annotation key exists on a resource
func IsSpecifiedAnnotationKeyExist(oc *CLI, resource, namespace, annotationKey string) bool {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "-o=jsonpath={.metadata.annotations}")
	annotations, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	if err != nil {
		return false
	}
	return strings.Contains(annotations, annotationKey)
}

// AddAnnotationsToSpecificResource adds annotations to a specific resource
func AddAnnotationsToSpecificResource(oc *CLI, resource, namespace, annotation string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotation, "--overwrite")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// RemoveAnnotationFromSpecificResource removes an annotation from a specific resource
func RemoveAnnotationFromSpecificResource(oc *CLI, resource, namespace, annotationKey string) error {
	args := []string{resource}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, annotationKey+"-")
	return oc.AsAdmin().WithoutNamespace().Run("annotate").Args(args...).Execute()
}

// DebugNodeWithOptionsAndChroot launches debug container using chroot and with options e.g. --image
func DebugNodeWithOptionsAndChroot(oc *CLI, nodeName string, options []string, cmd ...string) (string, error) {
	stdOut, stdErr, err := debugNode(oc, nodeName, options, true, true, cmd...)
	return strings.Join([]string{stdOut, stdErr}, "\n"), err
}

// debugNode is the core function for launching debug containers
func debugNode(oc *CLI, nodeName string, cmdOptions []string, needChroot bool, recoverNsLabels bool, cmd ...string) (stdOut string, stdErr string, err error) {
	var (
		debugNodeNamespace string
		isNsPrivileged     bool
		cargs              []string
		outputError        error
	)

	cargs = []string{"node/" + nodeName}

	// Enhance for debug node namespace used logic
	// if "--to-namespace=" option is used, then uses the input options' namespace, otherwise use oc.Namespace()
	// if oc.Namespace() is empty, uses "default" namespace instead
	hasToNamespaceInCmdOptions, index := StringsSliceElementsHasPrefix(cmdOptions, "--to-namespace=", false)
	if hasToNamespaceInCmdOptions {
		debugNodeNamespace = strings.TrimPrefix(cmdOptions[index], "--to-namespace=")
	} else {
		debugNodeNamespace = oc.Namespace()
		if debugNodeNamespace == "" {
			debugNodeNamespace = "default"
		}
	}

	// Running oc debug node command in normal projects
	// (normal projects mean projects that are not clusters default projects like: "openshift-xxx" et al)
	// need extra configuration on 4.12+ ocp test clusters
	// https://github.com/openshift/oc/blob/master/pkg/helpers/cmd/errors.go#L24-L29
	if !strings.HasPrefix(debugNodeNamespace, "openshift-") {
		isNsPrivileged, outputError = IsNamespacePrivileged(oc, debugNodeNamespace)
		if outputError != nil {
			return "", "", outputError
		}
		if !isNsPrivileged {
			if recoverNsLabels {
				defer RecoverNamespaceRestricted(oc, debugNodeNamespace)
			}
			outputError = SetNamespacePrivileged(oc, debugNodeNamespace)
			if outputError != nil {
				return "", "", outputError
			}
		}
	}

	// For default nodeSelector enabled test clusters we need to add the extra annotation to avoid the debug pod's
	// nodeSelector overwritten by the scheduler
	if IsDefaultNodeSelectorEnabled(oc) && !IsWorkerNode(oc, nodeName) && !IsSpecifiedAnnotationKeyExist(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector`) {
		AddAnnotationsToSpecificResource(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector=`)
		defer RemoveAnnotationFromSpecificResource(oc, "ns/"+debugNodeNamespace, "", `openshift.io/node-selector`)
	}

	if len(cmdOptions) > 0 {
		cargs = append(cargs, cmdOptions...)
	}
	if !hasToNamespaceInCmdOptions {
		cargs = append(cargs, "--to-namespace="+debugNodeNamespace)
	}
	if needChroot {
		cargs = append(cargs, "--", "chroot", "/host")
	} else {
		cargs = append(cargs, "--")
	}
	cargs = append(cargs, cmd...)

	return oc.AsAdmin().WithoutNamespace().Run("debug").Args(cargs...).Outputs()
}
