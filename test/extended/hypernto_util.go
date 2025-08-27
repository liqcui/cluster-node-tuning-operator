package nto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

type HostedClusterPlatformType = string

const (
	// AWSPlatform represents Amazon Web Services infrastructure.
	AWSPlatform HostedClusterPlatformType = "AWS"

	// NonePlatform represents user supplied (e.g. bare metal) infrastructure.
	NonePlatform HostedClusterPlatformType = "None"

	// IBMCloudPlatform represents IBM Cloud infrastructure.
	IBMCloudPlatform HostedClusterPlatformType = "IBMCloud"

	// AgentPlatform represents user supplied insfrastructure booted with agents.
	AgentPlatform HostedClusterPlatformType = "Agent"

	// KubevirtPlatform represents Kubevirt infrastructure.
	KubevirtPlatform HostedClusterPlatformType = "KubeVirt"

	// AzurePlatform represents Azure infrastructure.
	AzurePlatform HostedClusterPlatformType = "Azure"

	// PowerVSPlatform represents PowerVS infrastructure.
	PowerVSPlatform HostedClusterPlatformType = "PowerVS"

	// Default polling intervals
	defaultPollInterval      = 15 * time.Second
	defaultPollTimeout       = 180 * time.Second
	fastPollInterval         = 10 * time.Second
	fastPollTimeout          = 30 * time.Second
	slowPollInterval         = 120 * time.Second
	hostedClusterPollInerval = 20 * time.Second
	hostedClusterPollTimeout = 10 * time.Minute
)

// IsROSA checks if the environment is ROSA (Red Hat OpenShift Service on AWS)
func IsROSA() bool {
	sharedDir := os.Getenv("SHARED_DIR")
	if sharedDir == "" {
		return false
	}

	data, err := os.ReadFile(sharedDir + "/cluster-type")
	if err != nil {
		return false
	}

	return strings.Contains(strings.ToLower(string(data)), "rosa")
}

// GetRandomString generates a random string for unique identifiers
func GetRandomString() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// IsHypershiftHostedCluster checks if the current cluster is a Hypershift hosted cluster
func IsHypershiftHostedCluster(oc *CLI) bool {
	// Check if this is a hosted cluster by trying to get hostedclusters resource
	// In a hosted cluster, this should fail because hosted clusters are managed from the management cluster
	_, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("hostedclusters", "-A", "--ignore-not-found").Output()
	// If the command succeeds with no error or just says resource not found, it's not a hosted cluster
	// Hosted clusters shouldn't have access to hostedclusters CRD at all
	return err != nil && strings.Contains(err.Error(), "the server doesn't have a resource type")
}

// AssertWaitPollNoErr asserts that wait.Poll did not return an error
func AssertWaitPollNoErr(err error, message string) {
	o.Expect(err).NotTo(o.HaveOccurred(), message)
}

// isHyperNTOPodInstalled will return true if any pod is found in the given namespace, and false otherwise
func isHyperNTOPodInstalled(oc *CLI, hostedClusterName string) bool {

	e2e.Logf("checking if pod is found in namespace %s...", hostedClusterName)
	deploymentList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", "-n", hostedClusterName, "-oname").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	deployNamesReg := regexp.MustCompile("cluster-node-tuning-operator")
	isNTOInstalled := deployNamesReg.MatchString(deploymentList)
	if !isNTOInstalled {
		e2e.Logf("no pod found in namespace %s :(", hostedClusterName)
		return false
	}
	e2e.Logf("pod found in namespace %s!", hostedClusterName)
	return true
}

// getNodePoolNamebyHostedClusterName used to get nodepool name in clusters
func getNodePoolNamebyHostedClusterName(oc *CLI, hostedClusterName, hostedClusterNS string) string {

	nodePoolNameList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", "-n", hostedClusterNS, "-ojsonpath='{.items[*].metadata.name}'").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(nodePoolNameList).NotTo(o.BeEmpty())

	//remove single quota from nodePoolNameList, then replace space with \n
	nodePoolNameStr := strings.Trim(nodePoolNameList, "'")
	nodePoolNameLines := strings.Replace(nodePoolNameStr, " ", "\n", -1)

	e2e.Logf("hosted cluster name is: %s", hostedClusterName)
	hostedClusterNameReg := regexp.MustCompile(".*" + hostedClusterName + ".*")
	nodePoolName := hostedClusterNameReg.FindAllString(nodePoolNameLines, -1)
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(nodePoolName).NotTo(o.BeEmpty())
	e2e.Logf("node pool name is: %s", nodePoolName[0])
	return nodePoolName[0]

}

// getTuningConfigMapNameWithRetry used to get tuned configmap name for specified node pool
func getTuningConfigMapNameWithRetry(oc *CLI, namespace string, filter string) string {
	var configmapName string

	err := wait.PollUntilContextTimeout(context.Background(), defaultPollInterval, defaultPollTimeout, false, func(ctx context.Context) (bool, error) {
		configMaps, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("configmap", "-n", namespace, "-oname").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(configMaps).NotTo(o.BeEmpty())

		//filter the tuned confimap name
		configMapsReg := regexp.MustCompile(".*" + filter + ".*")
		if !configMapsReg.MatchString(configMaps) {
			return false, nil
		}

		tuningConfigMap := configMapsReg.FindAllString(configMaps, -1)
		e2e.Logf("the list of tuned configmap is: \n%v", tuningConfigMap)

		//Node Pool using MC will have two configmap
		if len(tuningConfigMap) == 2 {
			configmapName = tuningConfigMap[0] + " " + tuningConfigMap[1]
		} else {
			configmapName = tuningConfigMap[0]
		}
		return true, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The value sysctl mismatch, please check")
	return configmapName
}

// getTunedSystemSetValueByParamNameInHostedCluster gets system value by parameter name in hosted cluster
func getTunedSystemSetValueByParamNameInHostedCluster(oc *CLI, ntoNamespace, nodeName, oscommand, sysctlparm string) string {
	var matchResult string

	err := wait.PollUntilContextTimeout(context.Background(), defaultPollInterval, defaultPollTimeout, false, func(ctx context.Context) (bool, error) {
		debugNodeStdout, err := oc.AsAdmin().AsGuestKubeconf().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+nodeName, "--", "chroot", "/host", oscommand, sysctlparm).Output()
		o.Expect(debugNodeStdout).NotTo(o.BeEmpty())

		if err == nil {
			regexpstr := regexp.MustCompile(sysctlparm + " =.*")
			matchResult = regexpstr.FindString(debugNodeStdout)
			e2e.Logf("the value of [ %v ] is [ %v ] on [ %v ]", sysctlparm, matchResult, nodeName)
			return true, nil
		}
		e2e.Logf("the debug node threw badrequest containercreating or other error, try next")
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "Fail to execute debug node, keep threw error BadRequest ContainerCreating, please check")
	return matchResult
}

// compareSpecifiedValueByNameOnLabelNodeWithRetryInHostedCluster compares specified value with retry
func compareSpecifiedValueByNameOnLabelNodeWithRetryInHostedCluster(oc *CLI, ntoNamespace, nodeName, oscommand, sysctlparm, specifiedvalue string) {
	expectedSettings := sysctlparm + " = " + specifiedvalue

	err := wait.PollUntilContextTimeout(context.Background(), defaultPollInterval, defaultPollTimeout, false, func(ctx context.Context) (bool, error) {
		tunedSettings := getTunedSystemSetValueByParamNameInHostedCluster(oc, ntoNamespace, nodeName, oscommand, sysctlparm)
		o.Expect(tunedSettings).NotTo(o.BeEmpty())

		return strings.Contains(tunedSettings, expectedSettings), nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The value sysctl mismatch, please check")
}

// assertIfTunedProfileAppliedOnSpecifiedNodeInHostedCluster checks if custom profile applied to a node
func assertIfTunedProfileAppliedOnSpecifiedNodeInHostedCluster(oc *CLI, namespace, tunedNodeName, expectedTunedName string) {
	err := wait.PollUntilContextTimeout(context.Background(), fastPollInterval, fastPollTimeout, false, func(ctx context.Context) (bool, error) {
		currentTunedName, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, "-ojsonpath={.status.tunedProfile}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(currentTunedName).NotTo(o.BeEmpty())
		e2e.Logf("the profile name on the node %v is: \n %v ", tunedNodeName, currentTunedName)

		expectedAppliedStatus, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(expectedAppliedStatus).NotTo(o.BeEmpty())
		expectedAppliedStatus = strings.Trim(expectedAppliedStatus, "'")

		if currentTunedName != expectedTunedName || expectedAppliedStatus != "True" {
			e2e.Logf("profile '%s' has not yet been applied to %s, currentTunedName is %s, expectedAppliedStatus is %s - retrying...", expectedTunedName, tunedNodeName, currentTunedName, expectedAppliedStatus)
			return false, nil
		}

		e2e.Logf("profile '%s' has been applied to %s - continuing...", expectedTunedName, tunedNodeName)
		tunedProfiles, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(expectedAppliedStatus).NotTo(o.BeEmpty())
		e2e.Logf("current profiles on each node : \n %v ", tunedProfiles)
		return true, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Profile was not applied to %s within timeout limit (30 seconds)", tunedNodeName))
}

// assertNTOPodLogsLastLinesInHostedCluster
func assertNTOPodLogsLastLinesInHostedCluster(oc *CLI, namespace string, ntoPod string, lineN string, timeDurationSec int, filter string) {

	var logLineStr []string

	err := wait.PollUntilContextTimeout(context.Background(), 15*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		//Remove err assert for SNO, the OCP will can not access temporily when master node restart or certificate key removed
		ntoPodLogs, _ := oc.AsAdmin().AsGuestKubeconf().Run("logs").Args("-n", namespace, ntoPod, "--tail="+lineN).Output()

		regNTOPodLogs, err := regexp.Compile(".*" + filter + ".*")
		o.Expect(err).NotTo(o.HaveOccurred())
		isMatch := regNTOPodLogs.MatchString(ntoPodLogs)
		if isMatch {
			logLineStr = regNTOPodLogs.FindAllString(ntoPodLogs, -1)
			e2e.Logf("the logs of nto pod %v is: \n%v", ntoPod, logLineStr[0])
			return true, nil
		}
		e2e.Logf("the keywords of nto pod isn't found, try next ...")
		return false, nil
	})
	if len(logLineStr) > 0 {
		e2e.Logf("The logs of nto pod %v is: \n%v", ntoPod, logLineStr[0])
	}

	o.Expect(err).NotTo(o.HaveOccurred(), "The tuned pod's log doesn't contain the keywords, please check")
}

// getTunedRenderInHostedCluster returns a string representation of the rendered for tuned in the given namespace
func getTunedRenderInHostedCluster(oc *CLI, namespace string) (string, error) {
	return oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "tuned", "rendered", "-ojsonpath={.spec.profile[*].name}").Output()
}

// assertIfTunedProfileAppliedOnNodePoolLevelInHostedCluster use to check if custom profile applied to a node
func assertIfTunedProfileAppliedOnNodePoolLevelInHostedCluster(oc *CLI, namespace string, nodePoolName string, expectedTunedName string) {

	var (
		matchTunedProfile    bool
		matchAppliedStatus   bool
		matchNum             int
		currentAppliedStatus string
	)

	err := wait.PollUntilContextTimeout(context.Background(), 5*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		nodeNames, err := getAllNodesByNodePoolNameInHostedCluster(oc, nodePoolName)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("the nodes in nodepool [%v] is:\n%v", nodePoolName, nodeNames)

		currentProfiles, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io").Output()
		e2e.Logf("the currentprofiles in nodepool [%v] is:\n%v", nodePoolName, currentProfiles)
		o.Expect(err).NotTo(o.HaveOccurred())

		matchNum = 0
		for i := 0; i < len(nodeNames); i++ {
			currentTunedName, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", nodeNames[i], "-ojsonpath={.status.tunedProfile}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(currentTunedName).NotTo(o.BeEmpty())
			matchTunedProfile = strings.Contains(currentTunedName, expectedTunedName)

			currentAppliedStatus, err = oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io", nodeNames[i], `-ojsonpath='{.status.conditions[?(@.type=="Applied")].status}'`).Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(currentAppliedStatus).NotTo(o.BeEmpty())
			matchAppliedStatus = strings.Contains(currentAppliedStatus, "True")
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(currentAppliedStatus).NotTo(o.BeEmpty())

			if matchTunedProfile && matchAppliedStatus {
				matchNum++
				e2e.Logf("profile '%s' matchs on  %s - match times is:%v", expectedTunedName, nodeNames[i], matchNum)

			}
		}

		if matchNum == len(nodeNames) {
			tunedProfiles, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("-n", namespace, "profiles.tuned.openshift.io").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			e2e.Logf("current profiles on each node : \n %v ", tunedProfiles)
			return true, nil
		}
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Profile was not applied to %s within timeout limit (30 seconds)", nodePoolName))
}

// compareSpecifiedValueByNameOnNodePoolLevelWithRetryInHostedCluster
func compareSpecifiedValueByNameOnNodePoolLevelWithRetryInHostedCluster(oc *CLI, ntoNamespace, nodePoolName, oscommand, sysctlparm, specifiedvalue string) {

	var (
		isMatch  bool
		matchNum int
	)

	err := wait.PollUntilContextTimeout(context.Background(), 15*time.Second, 180*time.Second, false, func(ctx context.Context) (bool, error) {
		nodeNames, err := getAllNodesByNodePoolNameInHostedCluster(oc, nodePoolName)
		o.Expect(err).NotTo(o.HaveOccurred())
		nodesNum := len(nodeNames)
		matchNum = 0
		//all worker node in the nodepool should match the tuned profile settings
		for i := 0; i < nodesNum; i++ {
			tunedSettings := getTunedSystemSetValueByParamNameInHostedCluster(oc, ntoNamespace, nodeNames[i], oscommand, sysctlparm)
			expectedSettings := sysctlparm + " = " + specifiedvalue
			if strings.Contains(tunedSettings, expectedSettings) {
				matchNum++
				isMatch = true
			}
		}
		if isMatch && matchNum == nodesNum {
			return true, nil
		}
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "The value sysctl mismatch, please check")
}

// assertMisMatchTunedSystemSettingsByParamNameOnNodePoolLevelInHostedCluster used to compare the the value shouldn't match specified name
func assertMisMatchTunedSystemSettingsByParamNameOnNodePoolLevelInHostedCluster(oc *CLI, ntoNamespace, nodePoolName, oscommand, sysctlparm, expectedMisMatchValue string) {
	nodeNames, err := getAllNodesByNodePoolNameInHostedCluster(oc, nodePoolName)
	o.Expect(err).NotTo(o.HaveOccurred())
	nodesNum := len(nodeNames)
	for i := 0; i < nodesNum; i++ {
		stdOut := getTunedSystemSetValueByParamNameInHostedCluster(oc, ntoNamespace, nodeNames[i], oscommand, sysctlparm)
		o.Expect(stdOut).NotTo(o.BeEmpty())
		o.Expect(stdOut).NotTo(o.ContainSubstring(expectedMisMatchValue))
	}
}

// assertIfMatchKenelBootOnNodePoolLevelInHostedCluster used to compare if match the keywords
func assertIfMatchKenelBootOnNodePoolLevelInHostedCluster(oc *CLI, ntoNamespace, nodePoolName, expectedMatchValue string, isMatch bool) {
	nodeNames, err := getAllNodesByNodePoolNameInHostedCluster(oc, nodePoolName)
	o.Expect(err).NotTo(o.HaveOccurred())

	nodesNum := len(nodeNames)
	for i := 0; i < nodesNum; i++ {
		err := wait.PollUntilContextTimeout(context.Background(), 15*time.Second, 180*time.Second, false, func(ctx context.Context) (bool, error) {
			debugNodeStdout, err := oc.AsAdmin().AsGuestKubeconf().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+nodeNames[i], "--", "chroot", "/host", "cat", "/proc/cmdline").Output()
			o.Expect(debugNodeStdout).NotTo(o.BeEmpty())

			if err == nil {
				e2e.Logf("the output of debug node is :\n%v)", debugNodeStdout)
				if isMatch {
					o.Expect(debugNodeStdout).To(o.ContainSubstring(expectedMatchValue))
				} else {
					o.Expect(debugNodeStdout).NotTo(o.ContainSubstring(expectedMatchValue))
				}
				return true, nil
			}
			e2e.Logf("the debug node threw badrequest containercreating or other error, try next")
			return false, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Fail to execute debug node, keep threw error BadRequest ContainerCreating, please check")
	}
}

// assertNTOPodLogsLastLinesInHostedCluster
func assertNTOPodLogsLastLinesInManagementCluster(oc *CLI, namespace string, ntoPod string, lineN string, timeDurationSec int, filter string) {

	var logLineStr []string

	err := wait.PollUntilContextTimeout(context.Background(), 15*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		//Remove err assert for SNO, the OCP will can not access temporily when master node restart or certificate key removed
		ntoPodLogs, _ := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", namespace, ntoPod, "--tail="+lineN).Output()

		regNTOPodLogs, err := regexp.Compile(".*" + filter + ".*")
		o.Expect(err).NotTo(o.HaveOccurred())
		isMatch := regNTOPodLogs.MatchString(ntoPodLogs)
		if isMatch {
			logLineStr = regNTOPodLogs.FindAllString(ntoPodLogs, -1)
			e2e.Logf("the logs of nto pod %v is: \n%v", ntoPod, logLineStr[0])
			return true, nil
		}
		e2e.Logf("the keywords of nto pod isn't found, try next ...")
		return false, nil
	})

	e2e.Logf("The logs of nto pod %v is: \n%v", ntoPod, logLineStr[0])
	o.Expect(err).NotTo(o.HaveOccurred(), "The tuned pod's log doesn't contain the keywords, please check")
}

// AssertIfNodeIsReadyByNodeNameInHostedCluster checks if the worker node is ready
func AssertIfNodeIsReadyByNodeNameInHostedCluster(oc *CLI, tunedNodeName string, timeDurationSec int) {

	o.Expect(timeDurationSec).Should(o.BeNumerically(">=", 10), "Disaster error: specify the value of timeDurationSec great than 10.")

	err := wait.PollUntilContextTimeout(context.Background(), time.Duration(timeDurationSec/10)*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		workerNodeStatus, err := oc.AsAdmin().AsGuestKubeconf().WithoutNamespace().Run("get").Args("nodes", tunedNodeName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(workerNodeStatus).NotTo(o.BeEmpty())

		if !strings.Contains(workerNodeStatus, "SchedulingDisabled") && strings.Contains(workerNodeStatus, "Ready") {
			e2e.Logf("the node [%v] status is %v in hosted clusters)", tunedNodeName, workerNodeStatus)
			return true, nil
		}
		e2e.Logf("worker node [%v] in hosted cluster checks failed, the worker node status should be ready)", tunedNodeName)
		return false, nil
	})
	o.Expect(err).NotTo(o.HaveOccurred(), "Worker node checks were not successful within timeout limit")

}

// AssertIfTunedIsReadyByNameInHostedCluster checks if the worker node is ready
func AssertIfTunedIsReadyByNameInHostedCluster(oc *CLI, tunedeName string, ntoNamespace string) {
	// Assert if profile applied to label node with re-try
	o.Eventually(func() bool {
		tunedStatus, err := oc.AsAdmin().AsGuestKubeconf().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		if err != nil || !strings.Contains(tunedStatus, tunedeName) {
			e2e.Logf("the tuned %s isn't generated, check again, err is %v", tunedeName, err)
		}
		e2e.Logf("the list of tuned in namespace %v is: \n%v", ntoNamespace, tunedStatus)
		return strings.Contains(tunedStatus, tunedeName)
	}, 5*time.Second, time.Second).Should(o.BeTrue())
}

// getAllNodesByNodePoolNameInHostedCluster gets all node names for a given node pool in a hosted cluster
func getAllNodesByNodePoolNameInHostedCluster(oc *CLI, nodePoolName string) ([]string, error) {
	nodeListStr, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("nodes", "-l", "hypershift.openshift.io/nodePool="+nodePoolName, "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return nil, err
	}
	nodeList := strings.Fields(strings.TrimSpace(nodeListStr))
	return nodeList, nil
}

// ValidHypershiftAndGetGuestKubeConf check if it is hypershift env and get kubeconf of the hosted cluster
// the first return is hosted cluster name
// the second return is the file of kubeconfig of the hosted cluster
// the third return is the hostedcluster namespace in mgmt cluster which contains the generated resources
// if it is not hypershift env, it will skip test.
func ValidHypershiftAndGetGuestKubeConf(oc *CLI) (string, string, string) {
	if IsROSA() {
		e2e.Logf("there is a rosa env")
		hostedClusterName, hostedclusterKubeconfig, hostedClusterNs := ROSAValidHypershiftAndGetGuestKubeConf(oc)
		if len(hostedClusterName) == 0 || len(hostedclusterKubeconfig) == 0 || len(hostedClusterNs) == 0 {
			g.Skip("there is a ROSA env, but the env is problematic, skip test run")
		}
		return hostedClusterName, hostedclusterKubeconfig, hostedClusterNs
	}
	operatorNS := GetHyperShiftOperatorNameSpace(oc)
	if len(operatorNS) <= 0 {
		g.Skip("there is no hypershift operator on host cluster, skip test run")
	}

	hostedclusterNS := GetHyperShiftHostedClusterNameSpace(oc)
	if len(hostedclusterNS) <= 0 {
		g.Skip("there is no hosted cluster NS in mgmt cluster, skip test run")
	}

	clusterNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"-n", hostedclusterNS, "hostedclusters", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	if len(clusterNames) <= 0 {
		g.Skip("there is no hosted cluster, skip test run")
	}

	hypersfhitPodStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"-n", operatorNS, "pod", "-l", "hypershift.openshift.io/operator-component=operator", "-l", "app=operator", "-o=jsonpath={.items[*].status.phase}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(hypersfhitPodStatus).To(o.ContainSubstring("Running"))

	//get first hosted cluster to run test
	e2e.Logf("the hosted cluster names: %s, and will select the first", clusterNames)
	clusterName := strings.Split(clusterNames, " ")[0]

	var hostedClusterKubeconfigFile string
	if os.Getenv("GUEST_KUBECONFIG") != "" {
		e2e.Logf("the kubeconfig you set guest_kubeconfig must be that of the hosted cluster %s in namespace %s", clusterName, hostedclusterNS)
		hostedClusterKubeconfigFile = os.Getenv("GUEST_KUBECONFIG")
		e2e.Logf("use a known hosted cluster kubeconfig: %v", hostedClusterKubeconfigFile)
	} else {
		// Check if hypershift command is available
		if _, err := exec.LookPath("hypershift"); err != nil {
			e2e.Logf("hypershift command not found, please set guest_kubeconfig environment variable or install hypershift cli")
			g.Skip("hypershift CLI not available and GUEST_KUBECONFIG not set, skipping test")
		}
		hostedClusterKubeconfigFile = "/tmp/guestcluster-kubeconfig-" + clusterName + "-" + GetRandomString()
		output, err := exec.Command("bash", "-c", fmt.Sprintf("hypershift create kubeconfig --name %s --namespace %s > %s",
			clusterName, hostedclusterNS, hostedClusterKubeconfigFile)).Output()
		e2e.Logf("the cmd output: %s", string(output))
		if err != nil {
			e2e.Logf("failed to create kubeconfig using hypershift cli: %v", err)
			g.Skip("failed to create kubeconfig using hypershift CLI, please set GUEST_KUBECONFIG environment variable")
		}
		e2e.Logf("create a new hosted cluster kubeconfig: %v", hostedClusterKubeconfigFile)
	}
	e2e.Logf("if you want hostedcluster controlplane namespace, you could get it by combining %s and %s with -", hostedclusterNS, clusterName)
	return clusterName, hostedClusterKubeconfigFile, hostedclusterNS
}

// ValidHypershiftAndGetGuestKubeConfWithNoSkip check if it is hypershift env and get kubeconf of the hosted cluster
// the first return is hosted cluster name
// the second return is the file of kubeconfig of the hosted cluster
// the third return is the hostedcluster namespace in mgmt cluster which contains the generated resources
// if it is not hypershift env, it will not skip the testcase and return null string.
func ValidHypershiftAndGetGuestKubeConfWithNoSkip(oc *CLI) (string, string, string) {
	if IsROSA() {
		e2e.Logf("there is a rosa env")
		return ROSAValidHypershiftAndGetGuestKubeConf(oc)
	}
	operatorNS := GetHyperShiftOperatorNameSpace(oc)
	if len(operatorNS) <= 0 {
		return "", "", ""
	}

	hostedclusterNS := GetHyperShiftHostedClusterNameSpace(oc)
	if len(hostedclusterNS) <= 0 {
		return "", "", ""
	}

	clusterNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"-n", hostedclusterNS, "hostedclusters", "-o=jsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	if len(clusterNames) <= 0 {
		return "", "", ""
	}

	hypersfhitPodStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"-n", operatorNS, "pod", "-l", "hypershift.openshift.io/operator-component=operator", "-l", "app=operator", "-o=jsonpath={.items[*].status.phase}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(hypersfhitPodStatus).To(o.ContainSubstring("Running"))

	//get first hosted cluster to run test
	e2e.Logf("the hosted cluster names: %s, and will select the first", clusterNames)
	clusterName := strings.Split(clusterNames, " ")[0]

	var hostedClusterKubeconfigFile string
	if os.Getenv("GUEST_KUBECONFIG") != "" {
		e2e.Logf("the kubeconfig you set guest_kubeconfig must be that of the guestcluster %s in namespace %s", clusterName, hostedclusterNS)
		hostedClusterKubeconfigFile = os.Getenv("GUEST_KUBECONFIG")
		e2e.Logf("use a known hosted cluster kubeconfig: %v", hostedClusterKubeconfigFile)
	} else {
		// Check if hypershift command is available
		if _, err := exec.LookPath("hypershift"); err != nil {
			e2e.Logf("hypershift command not found and guest_kubeconfig not set, returning empty values")
			return "", "", ""
		}
		hostedClusterKubeconfigFile = "/tmp/guestcluster-kubeconfig-" + clusterName + "-" + GetRandomString()
		output, err := exec.Command("bash", "-c", fmt.Sprintf("hypershift create kubeconfig --name %s --namespace %s > %s",
			clusterName, hostedclusterNS, hostedClusterKubeconfigFile)).Output()
		e2e.Logf("the cmd output: %s", string(output))
		if err != nil {
			e2e.Logf("failed to create kubeconfig using hypershift cli: %v, returning empty values", err)
			return "", "", ""
		}
		e2e.Logf("create a new hosted cluster kubeconfig: %v", hostedClusterKubeconfigFile)
	}
	e2e.Logf("if you want hostedcluster controlplane namespace, you could get it by combining %s and %s with -", hostedclusterNS, clusterName)
	return clusterName, hostedClusterKubeconfigFile, hostedclusterNS
}

// GetHyperShiftOperatorNameSpace get hypershift operator namespace
// if not exist, it will return empty string.
func GetHyperShiftOperatorNameSpace(oc *CLI) string {
	args := []string{
		"pods", "-A",
		"-l", "hypershift.openshift.io/operator-component=operator",
		"-l", "app=operator",
		"--ignore-not-found",
		"-ojsonpath={.items[0].metadata.namespace}",
	}
	namespace, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	return strings.TrimSpace(namespace)
}

// GetHyperShiftHostedClusterNameSpace get hypershift hostedcluster namespace
// if not exist, it will return empty string. If more than one exists, it will return the first one.
func GetHyperShiftHostedClusterNameSpace(oc *CLI) string {
	namespace, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"hostedcluster", "-A", "--ignore-not-found", "-ojsonpath={.items[*].metadata.namespace}").Output()

	if err != nil && !strings.Contains(namespace, "the server doesn't have a resource type") {
		o.Expect(err).NotTo(o.HaveOccurred(), "get hostedcluster fail: %v", err)
	}

	if len(namespace) <= 0 {
		return namespace
	}
	namespaces := strings.Fields(namespace)
	if len(namespaces) == 1 {
		return namespaces[0]
	}
	ns := ""
	for _, ns = range namespaces {
		if ns != "clusters" {
			break
		}
	}
	return ns
}

// ROSAValidHypershiftAndGetGuestKubeConf checks if it is ROSA-hypershift env and get kubeconf of the hosted cluster, only support prow
// the first return is hosted cluster name
// the second return is the file of kubeconfig of the hosted cluster
// the third return is the hostedcluster namespace in mgmt cluster which contains the generated resources
func ROSAValidHypershiftAndGetGuestKubeConf(oc *CLI) (string, string, string) {
	operatorNS := GetHyperShiftOperatorNameSpace(oc)
	if operatorNS == "" {
		e2e.Logf("there is no hypershift operator on host cluster")
		return "", "", ""
	}

	sharedDir := os.Getenv("SHARED_DIR")
	data, err := os.ReadFile(sharedDir + "/cluster-name")
	if err != nil {
		e2e.Logf("can't get hostedcluster name %s shared_dir: %s", err.Error(), sharedDir)
		return "", "", ""
	}

	clusterName := strings.ReplaceAll(string(data), "\n", "")
	hostedclusterNS, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("-A", "hostedclusters", `-o=jsonpath={.items[?(@.metadata.name=="`+clusterName+`")].metadata.namespace}`).Output()
	if hostedclusterNS == "" {
		e2e.Logf("there is no hosted cluster ns in mgmt cluster")
	}

	hostedClusterKubeconfigFile := sharedDir + "/nested_kubeconfig"
	return clusterName, hostedClusterKubeconfigFile, hostedclusterNS
}

// GetHostedClusterPlatformType returns a hosted cluster platform type
// oc is the management cluster client to query the hosted cluster platform type based on hostedcluster CR obj
func GetHostedClusterPlatformType(oc *CLI, clusterName, clusterNamespace string) (HostedClusterPlatformType, error) {
	if IsHypershiftHostedCluster(oc) {
		return "", fmt.Errorf("this is a hosted cluster env. You should use oc of the management cluster")
	}
	return oc.AsAdmin().WithoutNamespace().Run("get").Args("hostedcluster", clusterName, "-n", clusterNamespace, `-ojsonpath={.spec.platform.type}`).Output()
}

// GetNodePoolNamesbyHostedClusterName gets the nodepools names of the hosted cluster
func GetNodePoolNamesbyHostedClusterName(oc *CLI, hostedClusterName, hostedClusterNS string) []string {
	var nodePoolName []string
	nodePoolNameList, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", "-n", hostedClusterNS, "-ojsonpath={.items[*].metadata.name}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(nodePoolNameList).NotTo(o.BeEmpty())

	nodePoolName = strings.Fields(nodePoolNameList)
	e2e.Logf("\n\ngot nodepool(s) for the hosted cluster %s: %v\n", hostedClusterName, nodePoolName)
	return nodePoolName
}

// GetHostedClusterVersion gets a HostedCluster's version from the management cluster.
func GetHostedClusterVersion(mgmtOc *CLI, hostedClusterName, hostedClusterNs string) semver.Version {
	hcVersionStr, _, err := mgmtOc.
		AsAdmin().
		WithoutNamespace().
		Run("get").
		Args("hostedcluster", hostedClusterName, "-n", hostedClusterNs, `-o=jsonpath={.status.version.history[?(@.state!="")].version}`).
		Outputs()
	o.Expect(err).NotTo(o.HaveOccurred())

	hcVersion := semver.MustParse(hcVersionStr)
	e2e.Logf("found hosted cluster %s version = %q", hostedClusterName, hcVersion)
	return hcVersion
}

func CheckHypershiftOperatorExistence(mgmtOC *CLI) (bool, error) {
	stdout, _, err := mgmtOC.AsAdmin().WithoutNamespace().Run("get").
		Args("pods", "-n", "hypershift", "-o=jsonpath={.items[*].metadata.name}").Outputs()
	if err != nil {
		return false, fmt.Errorf("failed to get HO Pods: %v", err)
	}
	return len(stdout) > 0, nil
}

func SkipOnHypershiftOperatorExistence(mgmtOC *CLI, expectHO bool) {
	HOExist, err := CheckHypershiftOperatorExistence(mgmtOC)
	if err != nil {
		e2e.Logf("failed to check hypershift operator existence: %v, defaulting to not found", err)
	}

	if HOExist && !expectHO {
		g.Skip("Not expecting Hypershift Operator but it is found, skip the test")
	}
	if !HOExist && expectHO {
		g.Skip("Expecting Hypershift Operator but it is not found, skip the test")
	}
}

// WaitForHypershiftHostedClusterReady waits for the hostedCluster ready
func WaitForHypershiftHostedClusterReady(oc *CLI, hostedClusterName, hostedClusterNS string) {
	pollWaitErr := wait.PollUntilContextTimeout(context.Background(), hostedClusterPollInerval, hostedClusterPollTimeout, false, func(cxt context.Context) (bool, error) {
		hostedClusterAvailable, getStatusErr := oc.AsAdmin().WithoutNamespace().Run("get").Args("hostedclusters", "-n", hostedClusterNS, "--ignore-not-found", hostedClusterName, `-ojsonpath='{.status.conditions[?(@.type=="Available")].status}'`).Output()
		if getStatusErr != nil {
			e2e.Logf("failed to get hosted cluster %q status: %v, try next round", hostedClusterName, getStatusErr)
			return false, nil
		}
		if !strings.Contains(hostedClusterAvailable, "True") {
			e2e.Logf("hosted cluster %q status: available=%s, try next round", hostedClusterName, hostedClusterAvailable)
			return false, nil
		}

		hostedClusterProgressState, getStateErr := oc.AsAdmin().WithoutNamespace().Run("get").Args("hostedclusters", "-n", hostedClusterNS, "--ignore-not-found", hostedClusterName, `-ojsonpath={.status.version.history[?(@.state!="")].state}`).Output()
		if getStateErr != nil {
			e2e.Logf("failed to get hosted cluster %q progress state: %v, try next round", hostedClusterName, getStateErr)
			return false, nil
		}
		if !strings.Contains(hostedClusterProgressState, "Completed") {
			e2e.Logf("hosted cluster %q progress state: %q, try next round", hostedClusterName, hostedClusterProgressState)
			return false, nil
		}
		e2e.Logf("hosted cluster %q is ready now", hostedClusterName)
		return true, nil
	})
	AssertWaitPollNoErr(pollWaitErr, fmt.Sprintf("Hosted cluster %q still not ready", hostedClusterName))
}

// ValidHypershiftAndGetGuestKubeConf4SecondHostedCluster validates hypershift and gets guest kubeconfig for second hosted cluster
func ValidHypershiftAndGetGuestKubeConf4SecondHostedCluster(oc *CLI) (string, string, string) {
	// This function needs to be implemented based on hypershift cluster discovery logic
	// For now, return placeholder implementation
	e2e.Logf("validhypershiftandgetguestkubeconf4secondhostedcluster not fully implemented")
	return "", "", ""
}

// isAKSCluster checks if the cluster is an AKS cluster
func isAKSCluster(ctx context.Context, oc *CLI) (bool, error) {
	infraOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platform}").Output()
	if err != nil {
		return false, err
	}
	return strings.ToLower(strings.TrimSpace(infraOutput)) == "azure" || strings.Contains(strings.ToLower(infraOutput), "aks"), nil
}

// checkPlatform checks the platform type
func checkPlatform(oc *CLI) string {
	platform, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.type}").Output()
	if err != nil {
		e2e.Logf("error getting platform: %v", err)
		return ""
	}
	return strings.ToLower(strings.TrimSpace(platform))
}

// getFirstLinuxWorkerNodeInHostedCluster gets the first Linux worker node in a hosted cluster
func getFirstLinuxWorkerNodeInHostedCluster(oc *CLI) (string, error) {
	nodeListStr, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker=,kubernetes.io/os=linux", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	nodes := strings.Fields(strings.TrimSpace(nodeListStr))
	if len(nodes) == 0 {
		return "", fmt.Errorf("no Linux worker nodes found in hosted cluster")
	}
	return nodes[0], nil
}

// getPodNameInHostedCluster gets a pod name in a hosted cluster
func getPodNameInHostedCluster(oc *CLI, namespace, selector, nodeName string) (string, error) {
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

	podNames, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args(args...).Output()
	if err != nil {
		return "", err
	}
	pods := strings.Fields(strings.TrimSpace(podNames))
	if len(pods) == 0 {
		return "", fmt.Errorf("no pods found matching the criteria in hosted cluster")
	}
	return pods[0], nil
}

// getFirstWorkerNodeByNodePoolNameInHostedCluster gets the first worker node by node pool name
func getFirstWorkerNodeByNodePoolNameInHostedCluster(oc *CLI, nodePoolName string) (string, error) {
	nodeListStr, err := oc.AsAdmin().AsGuestKubeconf().Run("get").Args("nodes", "-l", "hypershift.openshift.io/nodePool="+nodePoolName, "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	nodes := strings.Fields(strings.TrimSpace(nodeListStr))
	if len(nodes) == 0 {
		return "", fmt.Errorf("no worker nodes found for nodepool %s in hosted cluster", nodePoolName)
	}
	return nodes[0], nil
}

// confirmIfNodePoolDeletedByNameInHostedCluster checks if a node pool has been deleted
func confirmIfNodePoolDeletedByNameInHostedCluster(oc *CLI, nodePoolName, hostedClusterNS string, timeDurationSec int) bool {

	var (
		isMatch bool
	)

	err := wait.PollUntilContextTimeout(context.Background(), 90*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		nodesStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("--ignore-not-found", "np", nodePoolName, `-ojsonpath='{.status.conditions[?(@.type=="Ready")].status}'`, "--namespace", hostedClusterNS).Output()

		o.Expect(err).ShouldNot(o.HaveOccurred())

		e2e.Logf("the nodepool ready status is %v ...", nodesStatus)

		if len(nodesStatus) <= 0 {
			isMatch = true
			return true, nil
		}

		return false, nil
	})

	AssertWaitPollNoErr(err, "The status of nodepool isn't ready")

	return isMatch
}

// CreateCustomNodePoolInHypershift creates a custom nodepool in hypershift
func CreateCustomNodePoolInHypershift(oc *CLI, cloudProvider, guestClusterName, nodePoolName, nodeCount, instanceType, upgradeType, clustersNS, defaultNodePoolName string) {

	if cloudProvider == "aws" {
		cmdString := fmt.Sprintf("hypershift create nodepool %s --cluster-name %s --name %s --node-count %s --instance-type %s --node-upgrade-type %s --namespace %s", cloudProvider, guestClusterName, nodePoolName, nodeCount, instanceType, upgradeType, clustersNS)
		e2e.Logf("cmdstring is %v )", cmdString)
		_, err := exec.Command("bash", "-c", cmdString).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else if cloudProvider == "azure" {
		subnetID, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.subnetID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		cmdString := fmt.Sprintf("hypershift create nodepool %s --cluster-name %s --name %s --node-count %s --instance-type %s --node-upgrade-type %s --nodepool-subnet-id %s --namespace %s", cloudProvider, guestClusterName, nodePoolName, nodeCount, instanceType, upgradeType, subnetID, clustersNS)
		e2e.Logf("cmdstring is %v )", cmdString)
		_, err = exec.Command("bash", "-c", cmdString).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else if cloudProvider == "aks" {
		subnetID, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.subnetID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		azMKPSKU, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.image.azureMarketplace.sku}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		azMKPVersion, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.image.azureMarketplace.version}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		azMKPOffer, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.image.azureMarketplace.offer}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		azMKPPublisher, err := oc.AsAdmin().Run("get").Args("-n", clustersNS, "nodepool", defaultNodePoolName, "-ojsonpath={.spec.platform.azure.image.azureMarketplace.publisher}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		cmdString := fmt.Sprintf("hypershift create nodepool azure --cluster-name %s --name %s --node-count %s --instance-type %s --node-upgrade-type %s --nodepool-subnet-id %s --namespace %s --marketplace-offer %s --marketplace-publisher %s --marketplace-sku %s --marketplace-version %s", guestClusterName, nodePoolName, nodeCount, instanceType, upgradeType, subnetID, clustersNS, azMKPOffer, azMKPPublisher, azMKPSKU, azMKPVersion)
		e2e.Logf("cmdstring is %v )", cmdString)
		_, err = exec.Command("bash", "-c", cmdString).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
	} else {
		e2e.Logf("unsupported cloud provider is %v )", cloudProvider)
	}
}

// AssertIfNodePoolIsReadyByName checks if the Nodepool is ready
func AssertIfNodePoolIsReadyByName(oc *CLI, nodePoolName string, timeDurationSec int, clustersNS string) {

	o.Expect(timeDurationSec).Should(o.BeNumerically(">=", 10), "Disaster error: specify the value of timeDurationSec great than 10.")

	err := wait.PollUntilContextTimeout(context.Background(), time.Duration(timeDurationSec/10)*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		var (
			isNodePoolReady   string
			isAllNodesHealthy string
			err               error
		)
		isAllNodesHealthy, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", nodePoolName, "-n", clustersNS, `-ojsonpath='{.status.conditions[?(@.type=="AllNodesHealthy")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isAllNodesHealthy).NotTo(o.BeEmpty())

		isNodePoolReady, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", nodePoolName, "-n", clustersNS, `-ojsonpath='{.status.conditions[?(@.type=="Ready")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isNodePoolReady).NotTo(o.BeEmpty())

		//For master node, only make sure one of master is ready.
		if strings.Contains(isNodePoolReady, "True") && strings.Contains(isAllNodesHealthy, "True") {
			return true, nil
		}
		e2e.Logf("node pool [%v] checks failed, the following values were found (read type should be true '%v')", nodePoolName, isNodePoolReady)
		return false, nil
	})
	AssertWaitPollNoErr(err, "Nodepool checks were not successful within timeout limit")
}

// AssertIfNodePoolUpdatingConfigByName checks if the Nodepool is ready
func AssertIfNodePoolUpdatingConfigByName(oc *CLI, nodePoolName string, timeDurationSec int, clustersNS string) {

	o.Expect(timeDurationSec).Should(o.BeNumerically(">=", 10), "Disaster error: specify the value of timeDurationSec great than 10.")

	err := wait.PollUntilContextTimeout(context.Background(), time.Duration(timeDurationSec/10)*time.Second, time.Duration(timeDurationSec)*time.Second, false, func(ctx context.Context) (bool, error) {

		var (
			isNodePoolUpdatingConfig  string
			isNodePoolAllNodesHealthy string
			isNodePoolReady           string
			err                       error
		)
		isNodePoolUpdatingConfig, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", nodePoolName, "-n", clustersNS, `-ojsonpath='{.status.conditions[?(@.type=="UpdatingConfig")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isNodePoolUpdatingConfig).NotTo(o.BeEmpty())

		isNodePoolAllNodesHealthy, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", nodePoolName, "-n", clustersNS, `-ojsonpath='{.status.conditions[?(@.type=="AllNodesHealthy")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isNodePoolAllNodesHealthy).NotTo(o.BeEmpty())

		isNodePoolReady, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodepool", nodePoolName, "-n", clustersNS, `-ojsonpath='{.status.conditions[?(@.type=="Ready")].status}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isNodePoolAllNodesHealthy).NotTo(o.BeEmpty())

		if !strings.Contains(isNodePoolUpdatingConfig, "True") && strings.Contains(isNodePoolAllNodesHealthy, "True") && strings.Contains(isNodePoolReady, "True") {
			e2e.Logf("node pool [%v] status isnodepoolupdatingconfig: %v isnodepoolallnodeshealthy: %v isnodepoolready: %v')", nodePoolName, isNodePoolUpdatingConfig, isNodePoolAllNodesHealthy, isNodePoolReady)
			return true, nil
		}
		e2e.Logf("node pool [%v] checks failed, the following values were found (ready type should be empty '%v')", nodePoolName, isNodePoolUpdatingConfig)
		return false, nil
	})
	AssertWaitPollNoErr(err, "Nodepool checks were not successful within timeout limit")
}
