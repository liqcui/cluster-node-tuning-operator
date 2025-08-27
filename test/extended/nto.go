package nto

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

var _ = g.Describe("[sig-tuning-node] PSAP should", func() {
	defer g.GinkgoRecover()

	var (
		oc                            = NewCLIWithoutNamespace("nto-test")
		ntoNamespace                  = "openshift-cluster-node-tuning-operator"
		ntoFixture                    = func(name string) string { return fixturePath("nto", name) }
		paoFixture                    = func(name string) string { return fixturePath("pao", name) }
		overrideFile                  = ntoFixture("override.yaml")
		podTestFile                   = ntoFixture("pod_test.yaml")
		podNginxFile                  = ntoFixture("pod-nginx.yaml")
		tunedNFConntrackMaxFile       = ntoFixture("tuned-nf-conntrack-max.yaml")
		hPPerformanceProfileFile      = ntoFixture("hp-performanceprofile.yaml")
		hpPerformanceProfilePatchFile = ntoFixture("hp-performanceprofile-patch.yaml")

		cgroupSchedulerBacklist      = ntoFixture("cgroup-scheduler-blacklist.yaml")
		cgroupSchedulerBestEffortPod = ntoFixture("cgroup-scheduler-besteffor-pod.yaml")
		ntoTunedDebugFile            = ntoFixture("nto-tuned-debug.yaml")
		ntoIRQSMPFile                = ntoFixture("default-irq-smp-affinity.yaml")
		ntoRealtimeFile              = ntoFixture("realtime.yaml")
		ntoMCPFile                   = ntoFixture("machine-config-pool.yaml")
		IPSFile                      = ntoFixture("ips.yaml")
		workerStackFile              = ntoFixture("worker-stack-tuned.yaml")
		paoPerformanceFile           = paoFixture("pao-performanceprofile.yaml")
		paoPerformancePatchFile      = paoFixture("pao-performance-patch.yaml")
		paoPerformanceFixpatchFile   = paoFixture("pao-performance-fixpatch.yaml")
		paoPerformanceOptimizeFile   = paoFixture("pao-performance-optimize.yaml")
		paoIncludePerformanceProfile = paoFixture("pao-include-performance-profile.yaml")
		paoWorkerCnfMCPFile          = paoFixture("pao-workercnf-mcp.yaml")
		paoWorkerOptimizeMCPFile     = paoFixture("pao-workeroptimize-mcp.yaml")
		hugepage100MPodFile          = ntoFixture("hugepage-100m-pod.yaml")
		hugepageMCPfile              = ntoFixture("hugepage-mcp.yaml")
		hugepageTunedBoottimeFile    = ntoFixture("hugepage-tuned-boottime.yaml")
		stalldTunedFile              = ntoFixture("stalld-tuned.yaml")
		openshiftNodePostgresqlFile  = ntoFixture("openshift-node-postgresql.yaml")
		netPluginFile                = ntoFixture("net-plugin-tuned.yaml")
		cloudProviderFile            = ntoFixture("cloud-provider-profile.yaml")
		nodeDiffCPUsTunedBootFile    = ntoFixture("node-diffcpus-tuned-bootloader.yaml")
		nodeDiffCPUsMCPFile          = ntoFixture("node-diffcpus-mcp.yaml")
		tuningMaxPidFile             = ntoFixture("tuning-maxpid.yaml")

		isNTO                 bool
		paoNamespace          = "openshift-performance-addon-operator"
		iaasPlatform          string
		ManualPickup          bool
		podShippedFile        string
		podSysctlFile         string
		ntoTunedPidMax        string
		customTunedProfile    string
		tunedNodeName         string
		ntoSysctlTemplate     string
		ntoDefered            string
		ntoDeferedUpdatePatch string
		err                   error
	)

	g.BeforeEach(func() {
		// ensure NTO operator is installed
		isNTO = isNTOPodInstalled(oc, ntoNamespace)
		// get IaaS platform
		platformOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platform}").Output()
		if err == nil {
			iaasPlatform = strings.ToLower(platformOutput)
		}
		e2e.Logf("Cloud provider is: %v", iaasPlatform)
		ManualPickup = false

		podShippedFile = ntoFixture("pod-shipped.yaml")
		podSysctlFile = ntoFixture("nto-sysctl-pod.yaml")
		ntoTunedPidMax = ntoFixture("nto-tuned-pidmax.yaml")
		customTunedProfile = ntoFixture("custom-tuned-profiles.yaml")
		ntoSysctlTemplate = ntoFixture("nto-sysctl-template.yaml")
		ntoDefered = ntoFixture("deferred-nto.yaml")
		ntoDeferedUpdatePatch = ntoFixture("deferred-nto-update-patch.yaml")
	})

	// author: liqcui@redhat.com
	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-29789-Sysctl parameters that set by tuned can be overwritten by parameters set via /etc/sysctl [Flaky]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		g.By("pick one worker node and one tuned pod on same node")
		workerNodeName, err := getFirstLinuxWorkerNode(oc)
		o.Expect(workerNodeName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Worker Node: %v", workerNodeName)
		tunedPodName, err := getPodName(oc, ntoNamespace, "openshift-app=tuned", workerNodeName)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Tuned Pod: %v", tunedPodName)

		g.By("check values set by /etc/sysctl on node and store the values")
		inotify, _, err := debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "cat", "/etc/sysctl.d/inotify.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(inotify).To(o.And(
			o.ContainSubstring("fs.inotify.max_user_watches"),
			o.ContainSubstring("fs.inotify.max_user_instances")))
		maxUserWatchesValue := getMaxUserWatchesValue(inotify)
		maxUserInstancesValue := getMaxUserInstancesValue(inotify)
		e2e.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesValue)
		e2e.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstancesValue)

		g.By("mount /etc/sysctl on node")
		_, err = remoteShPod(oc, ntoNamespace, tunedPodName, "mount")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check sysctl kernel.pid_max on node and store the value")
		kernel, _, err := debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernel).To(o.ContainSubstring("kernel.pid_max"))
		pidMaxValue := getKernelPidMaxValue(kernel)
		e2e.Logf("kernel.pid_max has value of: %v", pidMaxValue)

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuneds.tuned.openshift.io", "override").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", workerNodeName, "tuned.openshift.io/override-").Execute()

		//tuned can not override parameters set via /etc/sysctl{.conf,.d} when reapply_sysctl=true
		//  The settings in /etc/sysctl.d/inotify.conf as below
		//      fs.inotify.max_user_watches = 65536     =>Try to override to 163840 by tuned, expect the old value 65536
		//      fs.inotify.max_user_instances = 8192    =>Not override by tuned, expect the old value 8192
		//      kernel.pid_max = 4194304                =>Default value is 4194304
		//  The settings in custom tuned profile as below
		//      fs.inotify.max_user_watches = 163840    =>Try to override to 163840 by tuned, expect the old value 65536
		//      kernel.pid_max = 1048576                =>Override by tuned, expect the new value 1048576
		g.By("create new NTO CR with reapply_sysctl=true and label the node")
		//reapply_sysctl=true tuned can not override parameters set via /etc/sysctl{.conf,.d}
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", workerNodeName, "tuned.openshift.io/override=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", overrideFile, "REAPPLY_SYSCTL=true")

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, workerNodeName, "override")

		g.By("check value of fs.inotify.max_user_instances on node (set by sysctl, should be the same as before), expected value is 8192")
		maxUserInstanceCheck, _, err := debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_instances")
		e2e.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstanceCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserInstanceCheck).To(o.ContainSubstring(maxUserInstancesValue))

		g.By("check value of fs.inotify.max_user_watches on node (set by sysctl, should be the same as before),expected value is 65536")
		maxUserWatchesCheck, _, err := debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_watches")
		e2e.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserWatchesCheck).To(o.ContainSubstring(maxUserWatchesValue))

		g.By("check value of kernel.pid_max on node (set by override tuned, should be the same value of override custom profile), expected value is 1048576")
		pidMaxCheck, _, err := debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		e2e.Logf("kernel.pid_max has value of: %v", pidMaxCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pidMaxCheck).To(o.ContainSubstring("kernel.pid_max = 1048576"))

		//tuned can override parameters set via /etc/sysctl{.conf,.d} when reapply_sysctl=false
		//  The settings in /etc/sysctl.d/inotify.conf as below
		//      fs.inotify.max_user_watches = 65536     =>Try to override to 163840 by tuned, expect the old value 163840
		//      fs.inotify.max_user_instances = 8192    =>Not override by tuned, expect the old value 8192
		//      kernel.pid_max = 4194304                =>Default value is 4194304
		//  The settings in custom tuned profile as below
		//      fs.inotify.max_user_watches = 163840    =>Try to override to 163840 by tuned, expect the old value 163840
		//      kernel.pid_max = 1048576                =>Override by tuned, expect the new value 1048576

		g.By("create new CR with reapply_sysctl=true")
		//reapply_sysctl=true tuned can not override parameters set via /etc/sysctl{.conf,.d}
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", overrideFile, "REAPPLY_SYSCTL=false")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check value of fs.inotify.max_user_instances on node (set by sysctl, should be the same as before),expected value is 8192")
		maxUserInstanceCheck, _, err = debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_instances")
		e2e.Logf("fs.inotify.max_user_instances has value of: %v", maxUserInstanceCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserInstanceCheck).To(o.ContainSubstring(maxUserInstanceCheck))

		g.By("check value of fs.inotify.max_user_watches on node (set by sysctl, should be the same value of override custom profile), expected value is 163840")
		maxUserWatchesCheck, _, err = debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "fs.inotify.max_user_watches")
		e2e.Logf("fs.inotify.max_user_watches has value of: %v", maxUserWatchesCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(maxUserWatchesCheck).To(o.ContainSubstring("fs.inotify.max_user_watches = 163840"))

		g.By("check value of kernel.pid_max on node (set by override tuned, should be the same value of override custom profile), expected value is 1048576")
		pidMaxCheck, _, err = debugNodeWithOptionsAndChrootWithoutRecoverNsLabel(oc, workerNodeName, []string{"-q"}, "sysctl", "kernel.pid_max")
		e2e.Logf("kernel.pid_max has value of: %v", pidMaxCheck)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pidMaxCheck).To(o.ContainSubstring("kernel.pid_max = 1048576"))

	})

	// author: nweinber@redhat.com
	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-33237-Test NTO support for operatorapi Unmanaged state [Flaky]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		defer func() {
			g.By("remove custom profile (if not already removed) and patch default tuned back to Managed")
			_ = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max", "--ignore-not-found").Execute()
			_ = patchTunedState(oc, ntoNamespace, "default", "Managed")
		}()

		isSNO := isSNOCluster(oc)
		is3Master := is3MasterNoDedicatedWorkerNode(oc)
		var profileCheck string

		masterNodeName := getFirstMasterNodeName(oc)
		defaultMasterProfileName := getDefaultProfileNameOnMaster(oc, masterNodeName)

		g.By("create logging namespace")
		oc.SetupProject()
		loggingNamespace := oc.Namespace()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", loggingNamespace, "--ignore-not-found").Execute()

		g.By("patch default tuned to 'Unmanaged'")
		err := patchTunedState(oc, ntoNamespace, "default", "Unmanaged")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err := getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Unmanaged"))

		g.By("create new pod from CR and label it")
		createNsResourceFromTemplate(oc, loggingNamespace, "--ignore-unknown-parameters=true", "-f", podTestFile)
		err = labelPod(oc, loggingNamespace, "web", "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("wait for pod web is ready")
		assertPodToBeReady(oc, "web", loggingNamespace)

		g.By("get the tuned node and pod names")
		tunedNodeName, err := getPodNodeName(oc, loggingNamespace, "web")
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Tuned Node: %v", tunedNodeName)
		tunedPodName, err := getPodName(oc, ntoNamespace, "openshift-app=tuned", tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Tuned Pod: %v", tunedPodName)

		g.By("create new profile from CR")
		createNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", tunedNFConntrackMaxFile)

		g.By("all node's current profile is:")
		stdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Profile Name Per Nodes: %v", stdOut)

		logsCheck, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, "--tail=9", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(logsCheck).NotTo(o.ContainSubstring("nf-conntrack-max"))

		if isSNO || is3Master {
			profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		nodeList, err := getAllNodesbyOSType(oc, "linux")
		o.Expect(err).NotTo(o.HaveOccurred())
		nodeListSize := len(nodeList)
		for i := 0; i < nodeListSize; i++ {
			output, err := debugNodeWithChroot(oc, nodeList[i], "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
		}

		g.By("remove custom profile and pod and patch default tuned back to Managed")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", loggingNamespace, "pod", "web").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		err = patchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		g.By("create new pod from CR and label it")
		createNsResourceFromTemplate(oc, loggingNamespace, "--ignore-unknown-parameters=true", "-f", podTestFile)
		err = labelPod(oc, loggingNamespace, "web", "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get the tuned node and pod names")
		tunedNodeName, err = getPodNodeName(oc, loggingNamespace, "web")
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Tuned Node: %v", tunedNodeName)
		tunedPodName, err = getPodName(oc, ntoNamespace, "openshift-app=tuned", tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Tuned Pod: %v", tunedPodName)

		g.By("create new profile from CR")
		createNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", tunedNFConntrackMaxFile)

		g.By("all node's current profile is:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Profile Name Per Nodes: %v", stdOut)

		g.By("assert nf-conntrack-max applied to the node that web application run on it.")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "nf-conntrack-max")

		profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("nf-conntrack-max"))

		g.By("all node's current profile is:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Profile Name Per Nodes: %v", stdOut)

		// tuned nodes should have value of 1048578, others should be 1048576
		for i := 0; i < nodeListSize; i++ {
			output, err := debugNodeWithChroot(oc, nodeList[i], "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			if nodeList[i] == tunedNodeName {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048578"))
			} else {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
			}
		}

		g.By("change tuned state back to Unmanaged and delete custom tuned")
		err = patchTunedState(oc, ntoNamespace, "default", "Unmanaged")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Unmanaged"))
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "nf-conntrack-max").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("nf-conntrack-max"))

		g.By("assert the log contains recommended profile (nf-conntrack-max) matches current configuratio ")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "20", 180, `recommended profile \(nf-conntrack-max\) matches current configuration|static tuning from profile 'nf-conntrack-max' applied`)

		g.By("all node's current profile is:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Profile Name Per Nodes: %v", stdOut)

		// tuned nodes should have value of 1048578, others should be 1048576
		for i := 0; i < nodeListSize; i++ {
			output, err := debugNodeWithChroot(oc, nodeList[i], "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			if nodeList[i] == tunedNodeName {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048578"))
			} else {
				o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
			}
		}

		g.By("changed tuned state back to Managed")
		err = patchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		if isSNO || is3Master {
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, defaultMasterProfileName)
			profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node")
			profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		g.By("all node's current profile is:")
		stdOut, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Profile Name Per Nodes: %v", stdOut)

		for i := 0; i < nodeListSize; i++ {
			output, err := debugNodeWithChroot(oc, nodeList[i], "sysctl", "net.netfilter.nf_conntrack_max")
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(output).To(o.ContainSubstring("net.netfilter.nf_conntrack_max = 1048576"))
		}
	})

	// author: nweinber@redhat.com
	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-36881-Node Tuning Operator will provide machine config for the master machine config pool [Disruptive] [Slow]", func() {

		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		isOneMasterwithNWorker := isOneMasterWithNWorkerNodes(oc)

		if !isNTO || isSNO || isOneMasterwithNWorker {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		if ManualPickup {
			g.Skip("This is the test case that execute mannually in shared cluster ...")
		}

		defer func() {
			g.By("remove new tuning profile after test completion")
			err := oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuneds.tuned.openshift.io", "openshift-node-performance-hp-performanceprofile").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()

		g.By("add new tuning profile from CR")
		createNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", hPPerformanceProfileFile)

		g.By("verify new tuned profile was created")
		profiles, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profiles).To(o.ContainSubstring("openshift-node-performance-hp-performanceprofile"))

		g.By("get NTO pod name and check logs for priority warning")
		ntoPodName, err := getNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("NTO pod name: %v", ntoPodName)
		assertNTOPodLogsLastLines(oc, ntoNamespace, ntoPodName, "10", 180, `openshift-node-performance-hp-performanceprofile have the same priority 30.*please use a different priority for your custom profiles`)

		g.By("patch priority for openshift-node-performance-hp-performanceprofile tuned to 18")
		err = patchTunedProfile(oc, ntoNamespace, "openshift-node-performance-hp-performanceprofile", hpPerformanceProfilePatchFile)
		o.Expect(err).NotTo(o.HaveOccurred())
		tunedPriority, err := getTunedPriority(oc, ntoNamespace, "openshift-node-performance-hp-performanceprofile")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedPriority).To(o.Equal("18"))

		g.By("check Nodes for expected changes")
		masterNodeName := assertIfNodeSchedulingDisabled(oc)
		e2e.Logf("The master node %v has been rebooted", masterNodeName)

		g.By("check MachineConfigPool for expected changes")
		assertIfMCPChangesAppliedByName(oc, "master", 1800)

		g.By("ensure the settings took effect on the master nodes, only check the first rebooted nodes")
		assertIfMasterNodeChangesApplied(oc, masterNodeName)

		g.By("check MachineConfig kernel arguments for expected changes")
		mcCheck, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("mc").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(mcCheck).To(o.ContainSubstring("50-nto-master"))
		mcKernelArgCheck, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("mc/50-nto-master").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(mcKernelArgCheck).To(o.ContainSubstring("default_hugepagesz=2M"))
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-43173-NTO Cgroup Blacklist Pod should affine to default cpuset.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get how many cpus on the specified worker node
		g.By("get how many cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node don't have enough cpus - skipping test ...")
		}

		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("remove custom profile (if not already removed) and remove node label")
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "cgroup-scheduler-affinecpuset").Execute()

		defer func() {
			err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node-").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()

		g.By("label the specified linux node with label tuned-scheduler-node")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// setting cgroup_ps_blacklist=/kubepods\.slice/
		// the process belong the /kubepods\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		// the process doesn't belong the /kubepods\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N

		g.By("create NTO custom tuned profile cgroup-scheduler-affinecpuset")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cgroupSchedulerBacklist, "-p", "PROFILE_NAME=cgroup-scheduler-affinecpuset", `CGROUP_BLACKLIST=/kubepods\.slice/`)

		g.By("check if NTO custom tuned profile cgroup-scheduler-affinecpuset was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "cgroup-scheduler-affinecpuset")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verified the cpu allow list in cgroup black list for tuned ...")
		clusterVersion, _, err := getClusterVersion(oc)
		versionReg := regexp.MustCompile(`4.12|4.13`)
		o.Expect(err).NotTo(o.HaveOccurred())
		if versionReg.MatchString(clusterVersion) {
			o.Expect(assertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "openshift-tuned", nodeCPUCoresInt)).To(o.Equal(true))
		} else {
			o.Expect(assertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "tuned", nodeCPUCoresInt)).To(o.Equal(true))
		}

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verified the cpu allow list in cgroup black list for chronyd ...")
		o.Expect(assertProcessNOTInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "chronyd", nodeCPUCoresInt)).To(o.Equal(true))

	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-27491-Add own custom profile to tuned operator [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		ntoRes := ntoResource{
			name:        "user-max-mnt-namespaces",
			namespace:   ntoNamespace,
			template:    customTunedProfile,
			sysctlparm:  "user.max_mnt_namespaces",
			sysctlvalue: "142214",
		}

		masterNodeName := getFirstMasterNodeName(oc)
		defaultMasterProfileName := getDefaultProfileNameOnMaster(oc, masterNodeName)

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		e2e.Logf("ntoTestNS is %v", ntoTestNS)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()

		is3CPNoWorker := is3MasterNoDedicatedWorkerNode(oc)
		//Clean up the custom profile user-max-mnt-namespaces and unlabel the nginx pod
		defer ntoRes.delete(oc)

		//First choice to use [tests] image, the image mirrored by default in disconnected cluster
		//if don't have [tests] image in some environment, we can use hello-openshift as image
		//usually test imagestream shipped in all ocp and mirror the image in disconnected cluster by default
		// AppImageName := getImagestreamImageName(oc, "tests")
		// if len(AppImageName) == 0 {
		AppImageName := "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
		// }

		//Create a nginx web application pod
		g.By("create a nginx web pod in nto temp namespace")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", podShippedFile, "-p", "IMAGENAME="+AppImageName)

		//Check if nginx pod is ready
		assertPodToBeReady(oc, "nginx", ntoTestNS)

		//Get the node name in the same node as nginx app
		tunedNodeName, err := getPodNodeName(oc, ntoTestNS, "nginx")
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("tunedNodeName is %v", tunedNodeName)

		//Get the tuned pod name in the same node as nginx app
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		//Label pod nginx with tuned.openshift.io/elasticsearch=
		g.By("label nginx pod as tuned.openshift.io/elasticsearch=")
		err = labelPod(oc, ntoTestNS, "nginx", "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("apply new profile from CR")
		ntoRes.createTunedProfileIfNotExist(oc)

		g.By("check if new profile  user-max-mnt-namespaces applied to labeled node")
		//Verify if the new profile is applied
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "user-max-mnt-namespaces")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-mnt-namespaces"))

		g.By("assert static tuning from profile 'user-max-mnt-namespaces' applied in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 180, `static tuning from profile 'user-max-mnt-namespaces' applied|active and recommended profile \(user-max-mnt-namespaces\) match`)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("compare if the value user.max_mnt_namespaces in on node with labeled pod, should be 142214")
		compareSysctlValueOnSepcifiedNodeByName(oc, tunedNodeName, "user.max_mnt_namespaces", "", "142214")

		g.By("delete custom tuned profile user.max_mnt_namespaces")
		ntoRes.delete(oc)

		//Check if restore to default profile.
		isSNO := isSNOCluster(oc)
		if isSNO || is3CPNoWorker {
			g.By("the cluster is SNO or Compact Cluster")
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, defaultMasterProfileName)
			g.By("assert default profile applied in tuned pod log")
			assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 180, "'"+defaultMasterProfileName+"' applied|("+defaultMasterProfileName+") match")
			profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal(defaultMasterProfileName))
		} else {
			g.By("the cluster is regular OCP Cluster")
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node")
			g.By("assert profile 'openshift-node' applied in tuned pod log")
			assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 180, `static tuning from profile 'openshift-node' applied|active and recommended profile \(openshift-node\) match`)
			profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(profileCheck).To(o.Equal("openshift-node"))
		}

		g.By("check all nodes for user.max_mnt_namespaces value, all node should different from 142214")
		compareSysctlDifferentFromSpecifiedValueByName(oc, "user.max_mnt_namespaces", "142214")
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-NonPreRelease-Longduration-Author:liqcui-Medium-37125-Turning on debugging for tuned containers.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		ntoRes := ntoResource{
			name:        "user-max-net-namespaces",
			namespace:   ntoNamespace,
			template:    ntoTunedDebugFile,
			sysctlparm:  "user.max_net_namespaces",
			sysctlvalue: "101010",
		}

		var (
			isEnableDebug bool
			isDebugInLog  bool
		)

		//Clean up the custom profile user-max-mnt-namespaces
		defer ntoRes.delete(oc)

		//Create a temp namespace to deploy nginx pod
		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		e2e.Logf("ntoTestNS is %v", ntoTestNS)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()

		//First choice to use [tests] image, the image mirrored by default in disconnected cluster
		//if don't have [tests] image in some environment, we can use hello-openshift as image
		//usually test imagestream shipped in all ocp and mirror the image in disconnected cluster by default
		// AppImageName := getImagestreamImageName(oc, "tests")
		// if len(AppImageName) == 0 {
		AppImageName := "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
		// }

		//Create a nginx web application pod
		g.By("create a nginx web pod in nto temp namespace")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", podNginxFile, "-p", "IMAGENAME="+AppImageName)

		//Check if nginx pod is ready
		assertPodToBeReady(oc, "nginx", ntoTestNS)

		//Get the node name in the same node as nginx app
		tunedNodeName, err := getPodNodeName(oc, ntoTestNS, "nginx")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node as nginx app
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		//To reset tuned pod log, forcily to delete tuned pod
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", tunedPodName, "-n", ntoNamespace, "--ignore-not-found=true").Execute()

		//Label pod nginx with tuned.openshift.io/elasticsearch=
		g.By("label nginx pod as tuned.openshift.io/elasticsearch=")
		err = labelPod(oc, ntoTestNS, "nginx", "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Verify if debug was disabled by default
		g.By("check node profile debug settings, it should be debug: false")
		isEnableDebug = assertDebugSettings(oc, tunedNodeName, ntoNamespace, "false")
		o.Expect(isEnableDebug).To(o.Equal(true))

		//Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("apply new profile from CR with debug setting is false")
		ntoRes.createDebugTunedProfileIfNotExist(oc, false)

		//Verify if the new profile is applied
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "user-max-net-namespaces", "True")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-net-namespaces"))

		//Verify nto tuned logs
		g.By("check NTO tuned pod logs to confirm if user-max-net-namespaces applied")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 180, `'user-max-net-namespaces' applied|\(user-max-net-namespaces\) match`)
		//Verify if debug is false by CR setting
		g.By("check node profile debug settings, it should be debug: false")
		isEnableDebug = assertDebugSettings(oc, tunedNodeName, ntoNamespace, "false")
		o.Expect(isEnableDebug).To(o.Equal(true))

		//Check if the log contain debug, the expected result should be none
		g.By("check if tuned pod log contains debug key word, the expected result should be no DEBUG")
		isDebugInLog = assertOprPodLogsbyFilter(oc, tunedPodName, ntoNamespace, "DEBUG", 2)
		o.Expect(isDebugInLog).To(o.Equal(false))

		g.By("delete custom profile and will apply a new one ...")
		ntoRes.delete(oc)

		g.By("apply new profile from CR with debug setting is true")
		ntoRes.createDebugTunedProfileIfNotExist(oc, true)

		//Verify if the new profile is applied
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "user-max-net-namespaces", "True")
		profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("user-max-net-namespaces"))

		//Verify nto tuned logs
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 180, `'user-max-net-namespaces' applied|\(user-max-net-namespaces\) match`)

		//Verify if debug was enabled by CR setting
		g.By("check if the debug is true in node profile, the expected result should be true")
		isEnableDebug = assertDebugSettings(oc, tunedNodeName, ntoNamespace, "true")
		o.Expect(isEnableDebug).To(o.Equal(true))

		//The log shouldn't contain debug in log
		g.By("check if tuned pod log contains debug key word, the log should contain DEBUG")
		assertOprPodLogsbyFilterWithDuration(oc, tunedPodName, ntoNamespace, "DEBUG", 60, 2)
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-37415-Allow setting isolated_cores without touching the default_irq_affinity [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity-").Execute()

		g.By("label the node with default-irq-smp-affinity ")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/default-irq-smp-affinity=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the default values of /proc/irq/default_smp_affinity on worker nodes")

		//This test case must got the value of default_smp_affinity without warning information
		defaultSMPAffinity, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/irq/default_smp_affinity").Output()
		e2e.Logf("the default value of /proc/irq/default_smp_affinity without cpu affinity is: %v", defaultSMPAffinity)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultSMPAffinity).NotTo(o.BeEmpty())
		defaultSMPAffinity = strings.ReplaceAll(defaultSMPAffinity, ",", "")
		defaultSMPAffinityMask := getDefaultSMPAffinityBitMaskbyCPUCores(oc, tunedNodeName)
		o.Expect(defaultSMPAffinity).To(o.ContainSubstring(defaultSMPAffinityMask))

		e2e.Logf("the value of /proc/irq/default_smp_affinity: %v", defaultSMPAffinityMask)
		cpuBitsMask := convertCPUBitMaskToByte(defaultSMPAffinityMask)
		o.Expect(cpuBitsMask).NotTo(o.BeEmpty())

		ntoRes1 := ntoResource{
			name:        "default-irq-smp-affinity",
			namespace:   ntoNamespace,
			template:    ntoIRQSMPFile,
			sysctlparm:  "#default_irq_smp_affinity",
			sysctlvalue: "1",
		}

		defer ntoRes1.delete(oc)

		g.By("create default-irq-smp-affinity profile to enable isolated_cores=1")
		ntoRes1.createIRQSMPAffinityProfileIfNotExist(oc)

		g.By("check if new NTO profile was applied")
		ntoRes1.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes after enabling isolated_cores=1")
		isolatedcoresSMPAffinity, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/irq/default_smp_affinity").Output()
		isolatedcoresSMPAffinity = strings.ReplaceAll(isolatedcoresSMPAffinity, ",", "")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(isolatedcoresSMPAffinity).NotTo(o.BeEmpty())
		e2e.Logf("the value of default_smp_affinity after setting isolated_cores=1 is: %v", isolatedcoresSMPAffinity)

		g.By("verify if the value of /proc/irq/default_smp_affinity is affected by isolated_cores=1")
		//Isolate the second cpu cores, the default_smp_affinity should be changed
		isolatedCPU := convertIsolatedCPURange2CPUList("1")
		o.Expect(isolatedCPU).NotTo(o.BeEmpty())

		newSMPAffinityMask := assertIsolateCPUCoresAffectedBitMask(cpuBitsMask, isolatedCPU)
		o.Expect(newSMPAffinityMask).NotTo(o.BeEmpty())
		o.Expect(isolatedcoresSMPAffinity).To(o.ContainSubstring(newSMPAffinityMask))

		g.By("remove the old profile and create a new one later ...")
		ntoRes1.delete(oc)

		ntoRes2 := ntoResource{
			name:        "default-irq-smp-affinity",
			namespace:   ntoNamespace,
			template:    ntoIRQSMPFile,
			sysctlparm:  "default_irq_smp_affinity",
			sysctlvalue: "1",
		}

		defer ntoRes2.delete(oc)
		g.By("create default-irq-smp-affinity profile to enable default_irq_smp_affinity=1")
		ntoRes2.createIRQSMPAffinityProfileIfNotExist(oc)

		g.By("check if new NTO profile was applied")
		ntoRes2.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "default-irq-smp-affinity", "True")

		g.By("check values of /proc/irq/default_smp_affinity on worker nodes")
		//We only need to return the value /proc/irq/default_smp_affinity without stdErr
		IRQSMPAffinity, _, err := debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"--quiet=true", "--to-namespace=" + ntoNamespace}, "cat", "/proc/irq/default_smp_affinity")
		IRQSMPAffinity = strings.ReplaceAll(IRQSMPAffinity, ",", "")
		o.Expect(IRQSMPAffinity).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		//Isolate the second cpu cores, the default_smp_affinity should be changed
		e2e.Logf("the value of default_smp_affinity after setting default_irq_smp_affinity=1 is: %v", IRQSMPAffinity)
		isMatch := assertDefaultIRQSMPAffinityAffectedBitMask(cpuBitsMask, isolatedCPU, string(IRQSMPAffinity))
		o.Expect(isMatch).To(o.Equal(true))
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-23958-Test NTO for label pod in daemon mode [Disruptive]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		ntoRes := ntoResource{
			name:        "user-max-ipc-namespaces",
			namespace:   ntoNamespace,
			template:    customTunedProfile,
			sysctlparm:  "user.max_ipc_namespaces",
			sysctlvalue: "121112",
		}
		defer func() {
			g.By("remove custom profile (if not already removed) and patch default tuned back to Managed")
			ntoRes.delete(oc)
		}()

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer func() {
			g.By("forcily remove label from the pod on first worker node in case compareSysctlDifferentFromSpecifiedValueByName step failure")
			err = labelPod(oc, ntoNamespace, tunedPodName, "tuned.openshift.io/elasticsearch-")
		}()

		g.By("apply new profile from CR")
		ntoRes.createTunedProfileIfNotExist(oc)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check all nodes for user.max_ipc_namespaces value, all node should different from 121112")
		compareSysctlDifferentFromSpecifiedValueByName(oc, "user.max_ipc_namespaces", "121112")

		g.By("label tuned pod as tuned.openshift.io/elasticsearch=")
		err = labelPod(oc, ntoNamespace, tunedPodName, "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "user-max-ipc-namespaces", "True")

		g.By("compare if the value user.max_ipc_namespaces in on node with labeled pod, should be 121112")
		compareSysctlValueOnSepcifiedNodeByName(oc, tunedNodeName, "user.max_ipc_namespaces", "", "121112")

		g.By("remove label from tuned pod as tuned.openshift.io/elasticsearch-")
		err = labelPod(oc, ntoNamespace, tunedPodName, "tuned.openshift.io/elasticsearch-")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check all nodes for user.max_ipc_namespaces value, all node should different from 121112")
		compareSysctlDifferentFromSpecifiedValueByName(oc, "user.max_ipc_namespaces", "121112")

	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-23959-Test NTO for remove pod in daemon mode [Disruptive]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		ntoRes := ntoResource{
			name:        "kernel-pid-max",
			namespace:   ntoNamespace,
			template:    customTunedProfile,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "128888",
		}
		defer func() {
			g.By("remove custom profile (if not already removed) and patch default tuned back to Managed")
			ntoRes.delete(oc)
			_ = patchTunedState(oc, ntoNamespace, "default", "Managed")
		}()

		isSNO := isSNOCluster(oc)
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer func() {
			g.By("forcily delete labeled pod on first worker node after test case executed in case compareSysctlDifferentFromSpecifiedValueByName step failure")
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", tunedPodName, "-n", ntoNamespace, "--ignore-not-found").Execute()
		}()

		g.By("apply new profile from CR")
		ntoRes.createTunedProfileIfNotExist(oc)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check all nodes for kernel.pid_max value, all node should different from 128888")
		compareSysctlDifferentFromSpecifiedValueByName(oc, "kernel.pid_max", "128888")

		g.By("label tuned pod as tuned.openshift.io/elasticsearch=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("pod", tunedPodName, "-n", ntoNamespace, "tuned.openshift.io/elasticsearch=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check if customized tuned profile applied on target node")
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "kernel-pid-max", "True")

		g.By("compare if the value kernel.pid_max in on node with labeled pod, should be 128888")
		compareSysctlValueOnSepcifiedNodeByName(oc, tunedNodeName, "kernel.pid_max", "", "128888")

		g.By("delete labeled tuned pod by name")
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", tunedPodName, "-n", ntoNamespace).Execute()

		g.By("check all nodes for kernel.pid_max value, all node should different from 128888")
		compareSysctlDifferentFromSpecifiedValueByName(oc, "kernel.pid_max", "128888")

	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-44650-NTO profiles provided with TuneD [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		//Get the tuned pod name that run on first worker node
		tunedNodeName, err := getFirstLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		g.By("check kernel version of worker nodes ...")
		kernelVersion, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.kernelVersion}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelVersion).NotTo(o.BeEmpty())

		g.By("check default tuned profile list, should contain openshift-control-plane and openshift-node")
		defaultTunedOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned", "default", "-ojsonpath={.spec.recommend}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedOutput).NotTo(o.BeEmpty())
		o.Expect(defaultTunedOutput).To(o.And(
			o.ContainSubstring("openshift-control-plane"),
			o.ContainSubstring("openshift-node")))

		g.By("check content of tuned file /usr/lib/tuned/openshift/tuned.conf to match default NTO settings")
		openshiftTunedConf, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftTunedConf).NotTo(o.BeEmpty())
		if strings.Contains(kernelVersion, "el8") || strings.Contains(kernelVersion, "el7") {
			o.Expect(openshiftTunedConf).To(o.And(
				o.ContainSubstring("avc_cache_threshold=8192"),
				o.ContainSubstring("kernel.pid_max=>4194304"),
				o.ContainSubstring("net.netfilter.nf_conntrack_max=1048576"),
				o.ContainSubstring("net.ipv4.conf.all.arp_announce=2"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("vm.max_map_count=262144"),
				o.ContainSubstring("/sys/module/nvme_core/parameters/io_timeout=4294967295"),
				o.ContainSubstring(`cgroup_ps_blacklist=/kubepods\.slice/`),
				o.ContainSubstring("runtime=0")))
		} else {
			o.Expect(openshiftTunedConf).To(o.And(
				o.ContainSubstring("avc_cache_threshold=8192"),
				o.ContainSubstring("nf_conntrack_hashsize=1048576"),
				o.ContainSubstring("kernel.pid_max=>4194304"),
				o.ContainSubstring("fs.aio-max-nr=>1048576"),
				o.ContainSubstring("net.netfilter.nf_conntrack_max=1048576"),
				o.ContainSubstring("net.ipv4.conf.all.arp_announce=2"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv4.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh1=8192"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh2=32768"),
				o.ContainSubstring("net.ipv6.neigh.default.gc_thresh3=65536"),
				o.ContainSubstring("vm.max_map_count=262144"),
				o.ContainSubstring("/sys/module/nvme_core/parameters/io_timeout=4294967295"),
				o.ContainSubstring(`cgroup_ps_blacklist=/kubepods\.slice/`),
				o.ContainSubstring("runtime=0")))
		}

		g.By("check content of tuned file /usr/lib/tuned/openshift-control-plane/tuned.conf to match default NTO settings")
		openshiftControlPlaneTunedConf, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-control-plane/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftControlPlaneTunedConf).NotTo(o.BeEmpty())
		o.Expect(openshiftControlPlaneTunedConf).To(o.ContainSubstring("include=openshift"))

		if strings.Contains(kernelVersion, "el8") || strings.Contains(kernelVersion, "el7") {
			o.Expect(openshiftControlPlaneTunedConf).To(o.And(
				o.ContainSubstring("sched_wakeup_granularity_ns=4000000"),
				o.ContainSubstring("sched_migration_cost_ns=5000000")))
		} else {
			o.Expect(openshiftControlPlaneTunedConf).NotTo(o.And(
				o.ContainSubstring("sched_wakeup_granularity_ns=4000000"),
				o.ContainSubstring("sched_migration_cost_ns=5000000")))
		}

		g.By("check content of tuned file /usr/lib/tuned/openshift-node/tuned.conf to match default NTO settings")
		openshiftNodeTunedConf, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-node/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftNodeTunedConf).To(o.And(
			o.ContainSubstring("include=openshift"),
			o.ContainSubstring("net.ipv4.tcp_fastopen=3"),
			o.ContainSubstring("fs.inotify.max_user_watches=65536"),
			o.ContainSubstring("fs.inotify.max_user_instances=8192")))
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-33238-Test NTO support for operatorapi Removed state [Disruptive]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		g.By("remove custom profile (if not already removed) and patch default tuned back to Managed")
		//Cleanup tuned and change back to managed state
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "tuned", "tuning-pidmax", "--ignore-not-found").Execute()
		defer patchTunedState(oc, ntoNamespace, "default", "Managed")

		ntoRes := ntoResource{
			name:        "tuning-pidmax",
			namespace:   ntoNamespace,
			template:    customTunedProfile,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "182218",
		}

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		e2e.Logf("ntoTestNS is %v", ntoTestNS)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()

		//Clean up the custom profile user-max-mnt-namespaces and unlabel the nginx pod
		defer ntoRes.delete(oc)

		//First choice to use [tests] image, the image mirrored by default in disconnected cluster
		//if don't have [tests] image in some environment, we can use hello-openshift as image
		//usually test imagestream shipped in all ocp and mirror the image in disconnected cluster by default
		// AppImageName := getImagestreamImageName(oc, "tests")
		// if len(AppImageName) == 0 {
		AppImageName := "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
		// }

		//Create a nginx web application pod
		g.By("create a nginx web pod in nto temp namespace")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", podNginxFile, "-p", "IMAGENAME="+AppImageName)

		//Check if nginx pod is ready
		assertPodToBeReady(oc, "nginx", ntoTestNS)

		//Get the node name in the same node as nginx app
		tunedNodeName, err := getPodNodeName(oc, ntoTestNS, "nginx")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node as nginx app
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		e2e.Logf("the tuned name on node %v is %v", tunedNodeName, tunedPodName)
		//Label pod nginx with tuned.openshift.io/elasticsearch=
		g.By("label nginx pod as tuned.openshift.io/elasticsearch=")
		err = labelPod(oc, ntoTestNS, "nginx", "tuned.openshift.io/elasticsearch=")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Apply new profile that match label tuned.openshift.io/elasticsearch=
		g.By("apply new profile from CR")
		ntoRes.createTunedProfileIfNotExist(oc)

		//Verify if the new profile is applied
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-pidmax"))

		g.By("check logs, profile changes SHOULD be applied since tuned is MANAGED")
		logsCheck, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, "--tail=9", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(logsCheck).To(o.ContainSubstring("tuning-pidmax"))

		g.By("compare if the value user.max_ipc_namespaces in on node with labeled pod, should be 182218")
		compareSysctlValueOnSepcifiedNodeByName(oc, tunedNodeName, "kernel.pid_max", "", "182218")

		g.By("patch default tuned to 'Removed'")
		err = patchTunedState(oc, ntoNamespace, "default", "Removed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err := getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Removed"))

		g.By("check logs, profiles, and nodes (profile changes SHOULD NOT be applied since tuned is REMOVED)")

		g.By("check pod status, all tuned pod should be terminated since tuned is REMOVED")
		waitForNoPodsAvailableByKind(oc, "daemonset", "tuned", ntoNamespace)
		podCheck, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "pods").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podCheck).NotTo(o.ContainSubstring("tuned"))

		g.By("check profile status, all node profile should be removed since tuned is REMOVED)")
		profileCheck, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Or(o.ContainSubstring("No resources"), o.BeEmpty()))

		g.By("change tuned state back to managed ...")
		err = patchTunedState(oc, ntoNamespace, "default", "Managed")
		o.Expect(err).NotTo(o.HaveOccurred())
		state, err = getTunedState(oc, ntoNamespace, "default")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(state).To(o.Equal("Managed"))

		g.By("get the tuned node and pod names")
		//Get the node name in the same node as nginx app
		tunedNodeName, err = getPodNodeName(oc, ntoTestNS, "nginx")
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node as nginx app
		tunedPodName = getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		g.By("check logs, profiles, and nodes (profile changes SHOULD be applied since tuned is MANAGED)")
		//Verify if the new profile is applied
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")
		profileCheck, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-pidmax"))

		g.By("check logs, profile changes SHOULD be applied since tuned is MANAGED)")
		logsCheck, err = oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, "--tail=9", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(logsCheck).To(o.ContainSubstring("tuning-pidmax"))

		g.By("compare if the value user.max_ipc_namespaces in on node with labeled pod, should be 182218")
		compareSysctlValueOnSepcifiedNodeByName(oc, tunedNodeName, "kernel.pid_max", "", "182218")
	})

	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-30589-NTO Use MachineConfigs to lay down files needed for tuned [Disruptive] [Slow]", func() {

		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer deleteMCAndMCPByName(oc, "50-nto-worker-rt", "worker-rt", 120)
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-rt-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-realtime", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("create machine config pool")
		applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", ntoMCPFile, "-p", "MCP_NAME=worker-rt")

		g.By("label the node with node-role.kubernetes.io/worker-rt=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-rt=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-realtime profile")
		//ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		// o.Expect(err).NotTo(o.HaveOccurred())
		// if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", ntoRealtimeFile, "-p", "INCLUDE=openshift-node,realtime")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert if machine config pool applied for worker nodes")
		assertIfMCPChangesAppliedByName(oc, "worker", 300)
		assertIfMCPChangesAppliedByName(oc, "worker-rt", 300)

		g.By("assert if openshift-realtime profile was applied ...")
		//Verify if the new profile is applied
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-realtime")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("openshift-realtime"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert if isolcpus was applied in machineconfig...")
		AssertTunedAppliedMC(oc, "nto-worker-rt", "isolcpus=")

		g.By("assert if isolcpus was applied in labled node...")
		isMatch := AssertTunedAppliedToNode(oc, tunedNodeName, "isolcpus=")
		o.Expect(isMatch).To(o.Equal(true))

		g.By("delete openshift-realtime tuned in labled node...")
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-realtime", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("check Nodes for expected changes")
		assertIfNodeSchedulingDisabled(oc)

		g.By("assert if machine config pool applied for worker nodes")
		assertIfMCPChangesAppliedByName(oc, "worker-rt", 300)

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert if isolcpus was applied in labled node...")
		isMatch = AssertTunedAppliedToNode(oc, tunedNodeName, "isolcpus=")
		o.Expect(isMatch).To(o.Equal(false))

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("delete custom MC and MCP by following right way...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-rt-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 300)
		deleteMCAndMCPByName(oc, "50-nto-worker-rt", "worker-rt", 120)
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-29804-Tuned profile is updated after incorrect tuned CR is fixed [Disruptive]", func() {
		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		is3Master := is3MasterNoDedicatedWorkerNode(oc)
		var (
			tunedNodeName string
			err           error
		)

		//Use the last worker node as labeled node
		//Support 3 master/worker node, no dedicated worker nodes
		if !is3Master && !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		e2e.Logf("tunedNodeName is:\n%v", tunedNodeName)

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "ips", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("label the node with tuned=ips")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned=ips", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create ips-host profile, new tuned should automatically handle duplicate sysctl settings")
		//Define duplicated parameter and value
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", IPSFile, "-p", "SYSCTLPARM1=kernel.pid_max", "SYSCTLVALUE1=1048575", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")

		g.By("assert recommended profile (ips-host) matches current configuration in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "15", 180, `recommended profile \(ips-host\) matches current configuration|\(ips-host\) match|'ips-host' applied`)

		g.By("check if new custom profile applied to label node")
		o.Expect(assertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "False")).To(o.Equal(true))

		//Only used for debug info
		g.By("check current profile for each node")
		output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		e2e.Logf("Current profile for each node: \n%v", output)

		//New tuned can automatically de-duplicate value of sysctl, no duplicate error anymore
		g.By("assert if the duplicate value of sysctl kernel.pid_max take effective on target node, expected value should be 1048575")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "kernel.pid_max", "1048575")

		g.By("get default value of fs.mount-max on label node")
		defaultMaxMapCount := getValueOfSysctlByName(oc, ntoNamespace, tunedNodeName, "fs.mount-max")
		o.Expect(defaultMaxMapCount).NotTo(o.BeEmpty())
		e2e.Logf("The default value of sysctl fs.mount-max is %v", defaultMaxMapCount)

		//setting an invalid value for ips-host profile
		g.By("update ips-host profile with invalid value of fs.mount-max = -1")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", IPSFile, "-p", "SYSCTLPARM1=fs.mount-max", "SYSCTLVALUE1=-1", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")

		g.By("assert static tuning from profile 'ips-host' applied in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "20", 180, `'ips-host' applied|recommended profile \(ips-host\) matches current configuration`)

		g.By("check if new custom profile applied to label node")
		o.Expect(assertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "True")).To(o.Equal(true))

		g.By("check current profile for each node")
		output, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		e2e.Logf("Current profile for each node: \n%v", output)

		//The invalid value won't impact default value of fs.mount-max
		g.By("assert if the value of sysctl fs.mount-max still use default value")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "fs.mount-max", defaultMaxMapCount)

		//setting an new value of fs.mount-max for ips-host profile
		g.By("update ips-host profile with new value of fs.mount-max = 868686")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", IPSFile, "-p", "SYSCTLPARM1=fs.mount-max", "SYSCTLVALUE1=868686", "SYSCTLPARM2=kernel.pid_max", "SYSCTLVALUE2=1048575")

		g.By("assert recommended profile (ips-host) matches current configuration in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "15", 180, `recommended profile \(ips-host\) matches current configuration|\(ips-host\) match|'ips-host' applied`)

		g.By("check if new custom profile applied to label node")
		o.Expect(assertNTOCustomProfileStatus(oc, ntoNamespace, tunedNodeName, "ips-host", "True", "False")).To(o.Equal(true))

		g.By("check current profile for each node")
		output, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		e2e.Logf("Current profile for each node: \n%v", output)

		//The invalid value won't impact default value of fs.mount-max
		g.By("assert if the new value of sysctl fs.mount-max take effective, expected value is 868686")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "fs.mount-max", "868686")
	})
	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-39123-NTO Operator will update tuned after changing included profile [Disruptive] [Slow]", func() {
		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		if ManualPickup {
			g.Skip("This is the test case that execute mannually in shared cluster ...")
		}

		skipPAODeploy := skipDeployPAO(oc)
		if skipPAODeploy {
			e2e.Logf("PAO deployment skipped and continue to execute test case")
		} else {
			g.Skip("PAO installation required; install the Performance Addon Operator before running this test")
		}

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer deleteMCAndMCPByName(oc, "50-nto-worker-cnf", "worker-cnf", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 360)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "performance-patch", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "performance", "--ignore-not-found").Execute()

		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-cnf-").Execute()

		g.By("label the node with node-role.kubernetes.io/worker-cnf=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-cnf=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// currently test is only supported on AWS, GCP, and Azure
		// if iaasPlatform == "aws" || iaasPlatform == "gcp" {

		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			//Only GCP and AWS support realtime-kenel
			g.By("apply performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoPerformanceFile, "-p", "ISENABLED=true")
		} else {
			g.By("apply performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoPerformanceFile, "-p", "ISENABLED=false")
		}

		g.By("apply worker-cnf machineconfigpool")
		applyOperatorResourceByYaml(oc, paoNamespace, paoWorkerCnfMCPFile)

		g.By("assert if the MCP worker-cnf has been successfully applied ...")
		assertIfMCPChangesAppliedByName(oc, "worker-cnf", 900)

		g.By("check if new NTO profile openshift-node-performance-performance was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-performance-performance")

		g.By("check if profile openshift-node-performance-performance applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-performance"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if tuned pod logs contains openshift-node-performance-performance on labeled nodes")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "20", 60, "openshift-node-performance-performance")

		g.By("check if the linux kernel parameter as vm.stat_interval = 10")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "vm.stat_interval", "10")

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("apply performance-patch profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, paoPerformancePatchFile)

		g.By("assert if the MCP worker-cnf is ready after node rebooted ...")
		assertIfMCPChangesAppliedByName(oc, "worker-cnf", 750)

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if profile what's active profile applied on nodes")
		nodeProfileName, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-performance"))

		g.By("check if tuned pod logs contains Cannot find profile 'openshift-node-performance-example-performanceprofile' on labeled nodes")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "30", 60, "Cannot find profile")

		g.By("check if the linux kernel parameter as vm.stat_interval = 1")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "vm.stat_interval", "1")

		g.By("patch include to include=openshift-node-performance-performance")
		err = patchTunedProfile(oc, ntoNamespace, "performance-patch", paoPerformanceFixpatchFile)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the MCP worker-cnf is ready after node rebooted ...")
		assertIfMCPChangesAppliedByName(oc, "worker-cnf", 600)

		g.By("check if new NTO profile performance-patch was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "performance-patch")

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if contains static tuning from profile 'performance-patch' applied in tuned pod logs on labeled nodes")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "30", 60, `static tuning from profile 'performance-patch' applied|recommended profile \(performance-patch\) matches current configuration`)

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if the linux kernel parameter as vm.stat_interval = 10")
		compareSpecifiedValueByNameOnLabelNode(oc, tunedNodeName, "vm.stat_interval", "10")

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("delete custom MC and MCP by following right way...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-cnf-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 300)
		deleteMCAndMCPByName(oc, "50-nto-worker-cnf", "worker-cnf", 120)
	})

	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-45686-NTO Creating tuned profile with references to not yet existing Performance Profile configuration.[Disruptive] [Slow]", func() {
		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		if ManualPickup {
			g.Skip("This is the test case that execute mannually in shared cluster ...")
		}

		skipPAODeploy := skipDeployPAO(oc)
		if skipPAODeploy {
			e2e.Logf("PAO deployment skipped and continue to execute test case")
		} else {
			g.Skip("PAO installation required; install the Performance Addon Operator before running this test")
		}

		defer deleteMCAndMCPByName(oc, "50-nto-worker-optimize", "worker-optimize", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 360)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "include-performance-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "optimize", "--ignore-not-found").Execute()

		//Use the last worker node as labeled node
		tunedNodeName, err := getLastLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-optimize-").Execute()

		g.By("label the node with node-role.kubernetes.io/worker-optimize=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-optimize=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply worker-optimize machineconfigpool")
		applyOperatorResourceByYaml(oc, paoNamespace, paoWorkerOptimizeMCPFile)

		g.By("assert if the MCP has been successfully applied ...")
		assertIfMCPChangesAppliedByName(oc, "worker-optimize", 600)

		isSNO = isSNOCluster(oc)
		if isSNO {
			g.By("apply include-performance-profile tuned profile")
			applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", paoIncludePerformanceProfile, "-p", "ROLENAME=master")
			g.By("assert if the mcp is ready after server has been successfully rebooted...")
			assertIfMCPChangesAppliedByName(oc, "master", 600)

		} else {
			g.By("apply include-performance-profile tuned profile")
			applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", paoIncludePerformanceProfile, "-p", "ROLENAME=worker-optimize")

			g.By("assert if the mcp is ready after server has been successfully rebooted...")
			assertIfMCPChangesAppliedByName(oc, "worker-optimize", 600)
		}

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if profile what's active profile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		if isSNO {
			o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-control-plane"))
		} else {
			o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node"))
		}

		g.By("check if tuned pod logs contains Cannot find profile 'openshift-node-performance-optimize' on labeled nodes")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 60, "Cannot find profile 'openshift-node-performance-optimize'")

		if isSNO {
			g.By("apply performance optimize profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoPerformanceOptimizeFile, "-p", "ROLENAME=master")
			g.By("assert if the mcp is ready after server has been successfully rebooted...")
			assertIfMCPChangesAppliedByName(oc, "master", 600)
		} else {
			g.By("apply performance optimize profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoPerformanceOptimizeFile, "-p", "ROLENAME=worker-optimize")
			g.By("assert if the mcp is ready after server has been successfully rebooted...")
			assertIfMCPChangesAppliedByName(oc, "worker-optimize", 600)
		}

		g.By("check performance profile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-optimize"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile performance-patch was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "include-performance-profile")

		g.By("check if profile what's active profile applied on nodes")
		nodeProfileName, err = getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("include-performance-profile"))

		g.By("check if contains static tuning from profile 'include-performance-profile' applied in tuned pod logs on labeled nodes")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "20", 60, `static tuning from profile 'include-performance-profile' applied|recommended profile \(include-performance-profile\) matches current configuration`)

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("delete custom MC and MCP by following right way...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-optimize-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 300)
		deleteMCAndMCPByName(oc, "50-nto-worker-optimize", "worker-optimize", 120)
	})

	g.It("NonHyperShiftHOST-Author:liqcui-Medium-36152-NTO Get metrics and alerts", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		//get metric information that require ssl auth
		sslKey := "/etc/prometheus/secrets/metrics-client-certs/tls.key"
		sslCrt := "/etc/prometheus/secrets/metrics-client-certs/tls.crt"

		//Get NTO metrics data
		g.By("get NTO metrics informaton without ssl, should be denied access, throw error...")
		metricsOutput, _ := oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", "openshift-monitoring", "sts/prometheus-k8s", "-c", "prometheus", "--", "curl", "-k", "https://node-tuning-operator.openshift-cluster-node-tuning-operator.svc:60000/metrics").Output()
		// o.Expect(metricsError).Should(o.HaveOccurred())
		o.Expect(metricsOutput).NotTo(o.BeEmpty())
		o.Expect(metricsOutput).To(o.Or(
			o.ContainSubstring("bad certificate"),
			o.ContainSubstring("errno = 104"),
			o.ContainSubstring("certificate required"),
			o.ContainSubstring("error:1409445C"),
			o.ContainSubstring("exit code 56"),
			o.ContainSubstring("Unauthorized"),
			o.ContainSubstring("errno = 32")))

		g.By("get NTO metrics informaton with ssl key and crt, should be access, get the metric information...")
		metricsOutput, metricsError := oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", "openshift-monitoring", "sts/prometheus-k8s", "-c", "prometheus", "--", "curl", "-k", "--key", sslKey, "--cert", sslCrt, "https://node-tuning-operator.openshift-cluster-node-tuning-operator.svc:60000/metrics").Output()
		o.Expect(metricsOutput).NotTo(o.BeEmpty())
		o.Expect(metricsError).NotTo(o.HaveOccurred())

		e2e.Logf("The metrics information of NTO as below: \n%v", metricsOutput)

		//Assert the key metrics
		g.By("check if all metrics exist as expected...")
		o.Expect(metricsOutput).To(o.And(
			o.ContainSubstring("nto_build_info"),
			o.ContainSubstring("nto_pod_labels_used_info"),
			o.ContainSubstring("nto_degraded_info"),
			o.ContainSubstring("nto_profile_calculated_total")))
	})

	g.It("NonPreRelease-Longduration-Author:liqcui-Medium-49265-NTO support automatically rotate ssl certificate. [Disruptive]", func() {
		// test requires NTO to be installed
		is3CPNoWorker := is3MasterNoDedicatedWorkerNode(oc)
		isSNO := isSNOCluster(oc)

		if !isNTO || is3CPNoWorker || isSNO {
			g.Skip("NTO is not installed or No need to test on compact cluster - skipping test ...")
		}

		//Use the last worker node as labeled node
		tunedNodeName, err = getLastLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("The tuned node name is: \n%v", tunedNodeName)

		//Get NTO operator pod name
		ntoOperatorPod, err := getNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The tuned operator pod name is: \n%v", ntoOperatorPod)

		metricEndpoint := getServiceENDPoint(oc, ntoNamespace)

		g.By("get information about the certificate the metrics server in NTO")
		openSSLOutputBefore, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "host", "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("get information about the creation and expiration date of the certificate")
		openSSLExpireDateBefore, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "host", "/bin/bash", "-c", "/bin/openssl s_client -connect "+metricEndpoint+" 2>/dev/null </dev/null | /bin/openssl x509 -noout -dates").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The openSSL Expired Date information of NTO openSSL before rotate as below: \n%v", openSSLExpireDateBefore)

		encodeBase64OpenSSLOutputBefore := stringToBASE64(openSSLOutputBefore)
		encodeBase64OpenSSLExpireDateBefore := stringToBASE64(openSSLExpireDateBefore)

		//To improve the sucessful rate, execute oc delete secret/node-tuning-operator-tls instead of oc -n openshift-service-ca secret/signing-key
		//The last one "oc -n openshift-service-ca secret/signing-key" take more time to complete, but need to manually execute once failed.
		g.By("delete secret/node-tuning-operator-tls to automate to create a new one certificate")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "secret/node-tuning-operator-tls").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert NTO logs to match key words restarting metrics server to rotate certificates")
		assertNTOPodLogsLastLines(oc, ntoNamespace, ntoOperatorPod, "4", 240, "restarting metrics server to rotate certificates")

		g.By("assert if NTO rotate certificates ...")
		AssertNTOCertificateRotate(oc, ntoNamespace, tunedNodeName, encodeBase64OpenSSLOutputBefore, encodeBase64OpenSSLExpireDateBefore)

		g.By("the certificate extracted from the openssl command should match the first certificate from the tls.crt file in the secret")
		compareCertificateBetweenOpenSSLandTLSSecret(oc, ntoNamespace, tunedNodeName)
	})

	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-49371-NTO will not restart tuned daemon when profile application take too long [Disruptive] [Slow]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		//The restart tuned has removed due to timeout in the bug https://issues.redhat.com/browse/OCPBUGS-30647
		//Use the first worker node as labeled node
		tunedNodeName, err := getFirstLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "worker-stuck-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-stuck", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("label the node with worker-stack=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "worker-stuck=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-profile-stuck profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, workerStackFile)

		g.By("check openshift-profile-stuck tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-profile-stuck"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert recommended profile (openshift-profile-stuck) matches current configuration in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "12", 300, `recommended profile \(openshift-profile-stuck\) matches current configuration|'openshift-profile-stuck' applied`)

		g.By("check if new NTO profile openshift-profile-stuck was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-profile-stuck")

		g.By("check if profile what's active profile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-profile-stuck"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("the log shouldn't contain [ timeout (120) to apply TuneD profile; restarting TuneD daemon ] in tuned pod log")
		ntoPodLogs, _ := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, tunedPodName, "--tail=10").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoPodLogs).NotTo(o.ContainSubstring("timeout (120) to apply TuneD profile; restarting TuneD daemon"))

		g.By("the log shouldn't contain [ error waiting for tuned: signal: terminated ] in tuned pod log")
		ntoPodLogs, _ = oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, tunedPodName, "--tail=10").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoPodLogs).NotTo(o.ContainSubstring("error waiting for tuned: signal: terminated"))
	})

	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-49370-NTO add huge pages to boot time via bootloader [Disruptive] [Slow]", func() {
		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or it's Single Node Cluster- skipping test ...")
		}

		//Use the last worker node as labeled node
		tunedNodeName, err := getLastLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node that labeled node
		//tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer deleteMCAndMCPByName(oc, "50-nto-worker-hp", "worker-hp", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 360)
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-hp-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "hugepages", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("label the node with node-role.kubernetes.io/worker-hp=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-hp=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create hugepages tuned profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, hugepageTunedBoottimeFile)

		g.By("check hugepages tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("hugepages"))

		g.By("create worker-hp machineconfigpool ...")
		applyOperatorResourceByYaml(oc, ntoNamespace, hugepageMCPfile)

		g.By("assert if the MCP has been successfully applied ...")
		assertIfMCPChangesAppliedByName(oc, "worker-hp", 720)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-hugepages")

		g.By("check if profile openshift-node-hugepages applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-hugepages"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check value of allocatable.hugepages-2Mi in labled node ")
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-2Mi}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeHugePagesOutput).To(o.ContainSubstring("100M"))

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		e2e.Logf("The nto test namespace is: \n%v", ntoTestNS)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()
		//First choice to use [tests] image, the image mirrored by default in disconnected cluster
		//if don't have [tests] image in some environment, we can use hello-openshift as image
		//usually test imagestream shipped in all ocp and mirror the image in disconnected cluster by default
		AppImageName := getImagestreamImageName(oc, "tests")
		if len(AppImageName) == 0 {
			AppImageName = "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
		}

		//Create a hugepages-app application pod
		g.By("create a hugepages-app pod to consume hugepage in nto temp namespace")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", hugepage100MPodFile, "-p", "IMAGENAME="+AppImageName)

		//Check if hugepages-appis ready
		g.By("check if a hugepages-app pod is ready ...")
		assertPodToBeReady(oc, "hugepages-app", ntoTestNS)

		g.By("check the value of /etc/podinfo/hugepages_2M_request, the value expected is 105 ...")
		podInfo, err := remoteShPod(oc, ntoTestNS, "hugepages-app", "cat", "/etc/podinfo/hugepages_2M_request")
		e2e.Logf("PodInfo is: \n%v", podInfo)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podInfo).To(o.ContainSubstring("105"))

		g.By("check the value of REQUESTS_HUGEPAGES in env on pod ...")
		envInfo, err := remoteShPodWithBash(oc, ntoTestNS, "hugepages-app", "env | grep REQUESTS_HUGEPAGES")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(envInfo).To(o.ContainSubstring("REQUESTS_HUGEPAGES_2Mi=104857600"))

		g.By("the right way to delete custom MC and MCP...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-hp-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 720)
		deleteMCAndMCPByName(oc, "50-nto-worker-hp", "worker-hp", 120)
	})

	g.It("NonPreRelease-Longduration-Author:liqcui-Medium-49439-NTO can start and stop stalld when relying on Tuned '[service]' plugin.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		if ManualPickup {
			g.Skip("This is the test case that execute mannually in shared cluster ...")
		}

		//Use the first rhcos worker node as labeled node
		tunedNodeName, err := getFirstLinuxWorkerNode(oc)
		e2e.Logf("tunedNodeName is [ %v ]", tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		if len(tunedNodeName) == 0 {
			g.Skip("Skip Testing on RHEL worker or windows node")
		}

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-stalld", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer debugNodeWithChroot(oc, tunedNodeName, "/usr/bin/throttlectl", "on")

		g.By("set off for /usr/bin/throttlectl before enable stalld")
		switchThrottlectlOnOff(oc, ntoNamespace, tunedNodeName, "off", 30)

		g.By("label the node with node-role.kubernetes.io/worker-stalld=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-stalld tuned profile")
		createNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", stalldTunedFile, "-p", "STALLD_STATUS=start,enable")

		g.By("check openshift-stalld tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-stalld")

		g.By("check if profile openshift-stalld applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if stalld service is running ...")
		stalldStatus, err := debugNodeWithChroot(oc, tunedNodeName, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))

		g.By("apply openshift-stalld with stop,disable tuned profile")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", stalldTunedFile, "-p", "STALLD_STATUS=stop,disable")

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-stalld")

		g.By("check if stalld service is inactive and stopped ...")
		stalldStatus, _ = debugNodeWithOptionsAndChroot(oc, tunedNodeName, []string{"-q", "--to-namespace", ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("inactive (dead)"))

		g.By("apply openshift-stalld with start,enable tuned profile")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", stalldTunedFile, "-p", "STALLD_STATUS=start,enable")

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-stalld")

		g.By("check if stalld service is running again ...")
		stalldStatus, _, err = debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace", ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))
	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-49441-NTO Applying a profile with multiple inheritance where parents include a common ancestor. [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		//trying to include two profiles that share the same parent profile "throughput-performance". An example of such profiles
		// are the openshift-node --> openshift --> (virtual-guest) --> throughput-performance and postgresql profiles.
		//Use the first worker node as labeled node

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if isMachineSetExist(oc) && !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/openshift-node-postgresql-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-node-postgresql", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("label the node with tuned.openshift.io/openshift-node-postgresql=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned.openshift.io/openshift-node-postgresql=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check postgresql profile /usr/lib/tuned/postgresql/tuned.conf include throughput-performance profile")
		postGreSQLProfile, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/postgresql/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(postGreSQLProfile).To(o.ContainSubstring("throughput-performance"))

		g.By("check postgresql profile /usr/lib/tuned/openshift-node/tuned.conf include openshift profile")
		openshiftNodeProfile, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift-node/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftNodeProfile).To(o.ContainSubstring(`include=openshift`))

		g.By("check postgresql profile /usr/lib/tuned/openshift/tuned.conf include throughput-performance profile")
		openshiftProfile, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/usr/lib/tuned/openshift/tuned.conf")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftProfile).To(o.ContainSubstring("throughput-performance"))

		g.By("create openshift-node-postgresql tuned profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, openshiftNodePostgresqlFile)

		g.By("check openshift-node-postgresql tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-postgresql"))

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-postgresql")

		g.By("check if profile openshift-node-postgresql applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-postgresql"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert recommended profile (openshift-node-postgresql) matches current configuration in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "10", 300, `recommended profile \(openshift-node-postgresql\) matches current configuration|static tuning from profile 'openshift-node-postgresql' applied`)
	})

	g.It("NonHyperShiftHOST-Author:liqcui-Medium-49705-Tuned net plugin handle net devices with n/a value for a channel. [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed or hosted cluster - skipping test ...")
		}

		if iaasPlatform == "vsphere" || iaasPlatform == "openstack" || iaasPlatform == "none" || iaasPlatform == "powervs" {
			g.Skip("IAAS platform: " + iaasPlatform + " doesn't support cloud provider profile - skipping test ...")
		}

		isSNO := isSNOCluster(oc)

		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		g.By("check default channel for host network adapter, not expected Combined: 1, if so, skip testing ...")
		//assertIFChannelQueuesStatus is used for checking if match Combined: 1
		//If match <Combined: 1>, skip testing
		isMatch := assertIFChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)
		if isMatch {
			g.Skip("Only one NIC queues or Unsupported NIC - skipping test ...")
		}

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("pod", tunedPodName, "-n", ntoNamespace, "node-role.kubernetes.io/netplugin-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "net-plugin", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("label the node with node-role.kubernetes.io/netplugin=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("pod", tunedPodName, "-n", ntoNamespace, "node-role.kubernetes.io/netplugin=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create net-plugin tuned profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, netPluginFile)

		g.By("check net-plugin tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("net-plugin"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert tuned.plugins.base: instance net: assigning devices match in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "180", 300, "tuned.plugins.base: instance net: assigning devices")

		g.By("assert active and recommended profile (net-plugin) match in tuned pod log")
		assertNTOPodLogsLastLines(oc, ntoNamespace, tunedPodName, "180", 300, `profile 'net-plugin' applied|profile \(net-plugin\) match`)

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "net-plugin")

		g.By("check if profile net-plugin applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(nodeProfileName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("net-plugin"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check channel for host network adapter, expected Combined: 1")
		o.Expect(assertIFChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)).To(o.BeTrue())

		g.By("delete tuned net-plugin and check channel for host network adapater again")
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "net-plugin", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("check if profile openshift-node|openshift-control-plane applied on nodes")
		if isSNO {
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-control-plane")
		} else {
			assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node")
		}

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check channel for host network adapter, not expected Combined: 1")
		o.Expect(assertIFChannelQueuesStatus(oc, ntoNamespace, tunedNodeName)).To(o.BeFalse())

	})

	g.It("ROSA-OSD_CCS-NonHyperShiftHOST-Author:liqcui-Medium-49617-NTO support cloud-provider specific profiles for NTO/TuneD. [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		if iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " doesn't support cloud provider profile - skipping test ...")
		}

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("get cloud provider name ...")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-abc", "-n", ntoNamespace, "--ignore-not-found").Execute()

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be 8192")
		sysctlOutput, err := remoteShPod(oc, ntoNamespace, tunedPodName, "sysctl", "vm.admin_reserve_kbytes")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(sysctlOutput).NotTo(o.BeEmpty())
		o.Expect(sysctlOutput).To(o.ContainSubstring("vm.admin_reserve_kbytes = 8192"))

		g.By("apply cloud-provider profile ...")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cloudProviderFile, "-p", "PROVIDER_NAME="+providerName)

		g.By("check /var/lib/tuned/provider on target nodes")
		openshiftProfile, err := remoteShPod(oc, ntoNamespace, tunedPodName, "cat", "/var/lib/ocp-tuned/provider")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(openshiftProfile).NotTo(o.BeEmpty())
		o.Expect(openshiftProfile).To(o.ContainSubstring(providerName))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check tuned for NTO")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned.tuned.openshift.io").Output()
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current tuned for NTO: \n%v", output)

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")

		g.By("remove cloud-provider profile, the value of vm.admin_reserve_kbytes rollback to 8192")
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be 8192")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "8192")

		g.By("apply cloud-provider-abc profile,the abc doesn't belong to any cloud provider ...")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cloudProviderFile, "-p", "PROVIDER_NAME=abc")

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value should be no change, still is 8192")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "8192")
	})

	g.It("Author:liqcui-Medium-45593-NTO Operator set io_timeout for AWS Nitro instances in correct way.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		// currently test is only supported on AWS
		if iaasPlatform == "aws" {
			g.By("expected /sys/module/nvme_core/parameters/io_timeout value on each node is: 4294967295")
			assertIOTimeOutandMaxRetries(oc, ntoNamespace)
		} else {
			g.Skip("Test Case 45593 doesn't support on other cloud platform, only support aws - skipping test ...")
		}

	})

	g.It("Author:liqcui-Medium-27420-NTO Operator is providing default tuned.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		defaultTunedCreateTimeBefore, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeBefore).NotTo(o.BeEmpty())

		g.By("delete the default tuned ...")
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "default", "-n", ntoNamespace).Execute()
		g.By("the make sure the tuned default created and ready")
		confirmedTunedReady(oc, ntoNamespace, "default", 60)

		defaultTunedCreateTimeAfter, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.BeEmpty())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.ContainSubstring(defaultTunedCreateTimeBefore))

		defaultTunedCreateTimeBefore, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(defaultTunedCreateTimeBefore).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		defaultTunedCreateTimeAfter, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "default", "-n", ntoNamespace, "-ojsonpath={.metadata.creationTimestamp}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(defaultTunedCreateTimeAfter).NotTo(o.BeEmpty())
		o.Expect(defaultTunedCreateTimeAfter).To(o.ContainSubstring(defaultTunedCreateTimeBefore))

		e2e.Logf("defaultTunedCreateTimeBefore is : %v defaultTunedCreateTimeAfter is: %v", defaultTunedCreateTimeBefore, defaultTunedCreateTimeAfter)

	})
	g.It("NonHyperShiftHOST-Author:liqcui-Medium-41552-NTO Operator Report per-node Tuned profile application status[Disruptive].", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		isSNO := isSNOCluster(oc)
		is3Master := is3MasterNoDedicatedWorkerNode(oc)
		masterNodeName := getFirstMasterNodeName(oc)
		defaultMasterProfileName := getDefaultProfileNameOnMaster(oc, masterNodeName)

		//NTO will provides two default tuned, one is openshift-control-plane, another is openshift-node
		g.By("check the default tuned profile list per nodes")
		profileOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace).Output()
		e2e.Logf("current profile on each node is:\n%s", profileOutput)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileOutput).NotTo(o.BeEmpty())
		if isSNO || is3Master {
			o.Expect(profileOutput).To(o.ContainSubstring(defaultMasterProfileName))
		} else {
			o.Expect(profileOutput).To(o.ContainSubstring("openshift-control-plane"))
			o.Expect(profileOutput).To(o.ContainSubstring("openshift-node"))
		}

	})

	g.It("NonHyperShiftHOST-Author:liqcui-Medium-50052-NTO RHCOS-shipped stalld systemd units should use SCHED_FIFO to run stalld[Disruptive].", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		if iaasPlatform == "vsphere" || iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " doesn't support cloud provider profile - skipping test ...")
		}

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		e2e.Logf("tunedNodeName is [ %v ]", tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())

		if len(tunedNodeName) == 0 {
			g.Skip("Skip Testing on RHEL worker or windows node")
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-stalld", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer debugNodeRetryWithOptionsAndChroot(oc, tunedNodeName, []string{"-q"}, "/usr/bin/throttlectl", "on")

		//Switch off throttlectl to improve sucessfull rate of stalld starting
		g.By("set off for /usr/bin/throttlectl before enable stalld")
		switchThrottlectlOnOff(oc, ntoNamespace, tunedNodeName, "off", 30)

		g.By("label the node with node-role.kubernetes.io/worker-stalld=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-stalld=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create openshift-stalld tuned profile")
		createNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", stalldTunedFile, "-p", "STALLD_STATUS=start,enable")

		g.By("check openshift-stalld tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-stalld")

		g.By("check if profile openshift-stalld applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).NotTo(o.BeEmpty())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-stalld"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if stalld service is running ...")
		stalldStatus, _, err := debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "systemctl", "status", "stalld")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldStatus).NotTo(o.BeEmpty())
		o.Expect(stalldStatus).To(o.ContainSubstring("active (running)"))

		g.By("get stalld PID on labeled node ...")
		stalldPIDStatus, _, err := debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "ps -efZ | grep stalld | grep -v grep")
		e2e.Logf("stalldPIDStatus is :\n%v", stalldPIDStatus)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldPIDStatus).NotTo(o.BeEmpty())
		o.Expect(stalldPIDStatus).NotTo(o.ContainSubstring("unconfined_service_t"))
		o.Expect(stalldPIDStatus).To(o.ContainSubstring("-t 20"))

		g.By("get stalld PID on labeled node ...")
		stalldPID, _, err := debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "ps -efL| grep stalld | grep -v grep | awk '{print $2}'")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stalldPID).NotTo(o.BeEmpty())

		g.By("get status of chrt -p stalld PID on labeled node ...")
		chrtStalldPIDOutput, _, err := debugNodeRetryWithOptionsAndChrootWithStdErr(oc, tunedNodeName, []string{"-q", "--to-namespace=" + ntoNamespace}, "/bin/bash", "-c", "chrt -ap "+stalldPID)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(chrtStalldPIDOutput).NotTo(o.BeEmpty())
		o.Expect(chrtStalldPIDOutput).To(o.ContainSubstring("SCHED_FIFO"))
		e2e.Logf("chrtStalldPIDOutput is :\n%v", chrtStalldPIDOutput)
	})
	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-51495-NTO PAO Shipped into NTO with basic function verification.[Disruptive][Slow].", func() {

		var (
			paoBaseProfileMCP = paoFixture("pao-baseprofile-mcp.yaml")
			paoBaseProfile    = paoFixture("pao-baseprofile.yaml")
			paoBaseQoSPod     = paoFixture("pao-baseqos-pod.yaml")
		)

		if ManualPickup {
			g.Skip("This is the test case that execute mannually in shared cluster ...")
		}

		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		skipPAODeploy := skipDeployPAO(oc)
		if skipPAODeploy {
			e2e.Logf("PAO deployment skipped and continue to execute test case")
		} else {
			g.Skip("PAO installation required; install the Performance Addon Operator before running this test")
		}

		defer deleteMCAndMCPByName(oc, "50-nto-worker-pao", "worker-pao", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 600)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()

		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get how many cpus on the specified worker node
		g.By("get how many cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node don't have enough cpus - skipping test ...")
		}
		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		// //Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()

		g.By("label the node with node-role.kubernetes.io/worker-pao=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// currently test is only supported on AWS, GCP, and Azure
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			//Only GCP and AWS support realtime-kenel
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=true")
		} else {
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=false")
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("create machine config pool worker-pao")
		applyOperatorResourceByYaml(oc, "", paoBaseProfileMCP)

		g.By("assert if machine config pool applied for worker nodes")
		assertIfMCPChangesAppliedByName(oc, "worker-pao", 1200)

		g.By("check openshift-node-performance-pao-baseprofile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check current profile openshift-node-performance-pao-baseprofile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile openshift-node-performance-pao-baseprofile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile")

		g.By("check if profile openshift-node-performance-pao-baseprofile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check value of allocatable.hugepages-1Gi in labled node ")
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-1Gi}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeHugePagesOutput).To(o.ContainSubstring("1Gi"))

		g.By("check Settings of CPU Manager policy created by PAO in labled node ")
		cpuManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep cpuManager").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerPolicy"))
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerReconcilePeriod"))
		e2e.Logf("The settings of CPU Manager Policy on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of CPU Manager for reservedSystemCPUs created by PAO in labled node ")
		cpuManagerConfOutput, err = oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep reservedSystemCPUs").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("reservedSystemCPUs"))
		e2e.Logf("The settings of CPU Manager reservedSystemCPUs on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of Topology Manager for topologyManagerPolicy created by PAO in labled node ")
		topologyManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep topologyManagerPolicy").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(topologyManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(topologyManagerConfOutput).To(o.ContainSubstring("topologyManagerPolicy"))
		e2e.Logf("The settings of CPU Manager topologyManagerPolicy on labeled nodes: \n%v", topologyManagerConfOutput)

		// currently test is only supported on AWS, GCP, and Azure
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).To(o.Or(o.ContainSubstring("rt")))
		} else {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).NotTo(o.Or(o.ContainSubstring("rt")))
		}

		g.By("check runtimeClass setting that created by PAO ... ")
		runtimeClassOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile", "pao-baseprofile", "-ojsonpath={.status.runtimeClass}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(runtimeClassOutput).NotTo(o.BeEmpty())
		o.Expect(runtimeClassOutput).To(o.ContainSubstring("performance-pao-baseprofile"))
		e2e.Logf("The settings of runtimeClass on labeled nodes: \n%v", runtimeClassOutput)

		g.By("check allocable system resouce on labeled node ... ")
		allocableResource, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(allocableResource).NotTo(o.BeEmpty())
		e2e.Logf("The allocable system resouce on labeled node: \n%v", allocableResource)

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()

		//Create a guaranteed-pod application pod
		g.By("create a guaranteed-pod pod into temp namespace")
		applyOperatorResourceByYaml(oc, ntoTestNS, paoBaseQoSPod)

		//Check if guaranteed-pod is ready
		g.By("check if a guaranteed-pod pod is ready ...")
		assertPodToBeReady(oc, "guaranteed-pod", ntoTestNS)

		g.By("check the cpu bind to isolation CPU zone for a guaranteed-pod")
		cpuManagerStateOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /var/lib/kubelet/cpu_manager_state").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerStateOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerStateOutput).To(o.ContainSubstring("guaranteed-pod"))
		e2e.Logf("The settings of CPU Manager cpuManagerState on labeled nodes: \n%v", cpuManagerStateOutput)

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("delete custom MC and MCP by following correct logic ...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 480)
		deleteMCAndMCPByName(oc, "50-nto-worker-pao", "worker-pao", 120)
	})

	g.It("NonHyperShiftHOST-Author:liqcui-Medium-53053-NTO will automatically delete profile with unknown/stuck state. [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		if iaasPlatform == "none" {
			g.Skip("IAAS platform: " + iaasPlatform + " doesn't support cloud provider profile - skipping test ...")
		}

		var (
			ntoUnknownProfile = ntoFixture("nto-unknown-profile.yaml")
		)

		//Get NTO operator pod name
		ntoOperatorPod, err := getNTOPodName(oc, ntoNamespace)
		o.Expect(ntoOperatorPod).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		isSNO := isSNOCluster(oc)
		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		g.By("get cloud provider name ...")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerName).NotTo(o.BeEmpty())

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("profiles.tuned.openshift.io", "worker-does-not-exist-openshift-node", "-n", ntoNamespace, "--ignore-not-found").Execute()

		g.By("apply worker-does-not-exist-openshift-node profile ...")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", ntoUnknownProfile, "-p", "PROVIDER_NAME="+providerName)

		g.By("the profile worker-does-not-exist-openshift-node will be deleted automatically once created.")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.ContainSubstring("worker-does-not-exist-openshift-node"))

		g.By("assert NTO logs to match key words  Node 'worker-does-not-exist-openshift-node' not found")
		assertNTOPodLogsLastLines(oc, ntoNamespace, ntoOperatorPod, "4", 120, " Node \"worker-does-not-exist-openshift-node\" not found")

	})

	g.It("NonPreRelease-Longduration-Author:liqcui-Medium-59884-NTO Cgroup Blacklist multiple regular expression. [Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()

		//Get the tuned pod name that run on first worker node
		tunedNodeName, err := getLastLinuxWorkerNode(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())

		//First choice to use [tests] image, the image mirrored by default in disconnected cluster
		//if don't have [tests] image in some environment, we can use hello-openshift as image
		//usually test imagestream shipped in all ocp and mirror the image in disconnected cluster by default
		// AppImageName := getImagestreamImageName(oc, "tests")
		// if len(AppImageName) == 0 {
		AppImageName := "quay.io/openshifttest/nginx-alpine@sha256:04f316442d48ba60e3ea0b5a67eb89b0b667abf1c198a3d0056ca748736336a0"
		// }

		//Get how many cpus on the specified worker node
		g.By("get how many cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node don't have enough cpus - skipping test ...")
		}

		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("remove custom profile (if not already removed) and remove node label")
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "cgroup-scheduler-blacklist").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node-").Execute()

		g.By("label the specified linux node with label tuned-scheduler-node")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "tuned-scheduler-node=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		// setting cgroup_ps_blacklist=/kubepods\.slice/kubepods-burstable\.slice/;/system\.slice/
		// the process belong the /kubepods\.slice/kubepods-burstable\.slice/ or /system\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		// the process doesn't belong the /kubepods\.slice/kubepods-burstable\.slice/ or /system\.slice/ can consume all cpuset
		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", "-n", ntoTestNS, "app-web", "--ignore-not-found").Execute()

		g.By("create pod that deletect the value of kernel.pid_max ")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", cgroupSchedulerBestEffortPod, "-p", "IMAGE_NAME="+AppImageName)

		//Check if nginx pod is ready
		g.By("check if best effort pod is ready...")
		assertPodToBeReady(oc, "app-web", ntoTestNS)

		g.By("create NTO custom tuned profile cgroup-scheduler-blacklist")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cgroupSchedulerBacklist, "-p", "PROFILE_NAME=cgroup-scheduler-blacklist", `CGROUP_BLACKLIST=/kubepods\.slice/kubepods-burstable\.slice/;/system\.slice/`)

		g.By("check if NTO custom tuned profile cgroup-scheduler-blacklist was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "cgroup-scheduler-blacklist")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verified the cpu allow list in cgroup black list for tuned ...")
		o.Expect(assertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "tuned", nodeCPUCoresInt)).To(o.Equal(true))

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0-N
		g.By("verified the cpu allow list in cgroup black list for chronyd ...")
		o.Expect(assertProcessInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "chronyd", nodeCPUCoresInt)).To(o.Equal(true))

		// The expected Cpus_allowed_list in /proc/$PID/status should be 0 or 0,2-N
		g.By("verified the cpu allow list in cgroup black list for nginx process...")
		o.Expect(assertProcessNOTInCgroupSchedulerBlacklist(oc, tunedNodeName, ntoNamespace, "nginx| tail -1", nodeCPUCoresInt)).To(o.Equal(true))
	})
	g.It("Longduration-NonPreRelease-Author:liqcui-Medium-60743-NTO No race to update MC when nodes with different number of CPUs are in the same MCP. [Disruptive] [Slow]", func() {

		// test requires NTO to be installed
		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		haveMachineSet := isMachineSetExist(oc)

		if !haveMachineSet {
			g.Skip("No machineset found, skipping test ...")
		}

		// currently test is only supported on AWS, GCP, Azure, ibmcloud, alibabacloud
		supportPlatforms := []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

		if !implStringArrayContains(supportPlatforms, iaasPlatform) {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test ...")
		}

		//Prior to choose worker nodes with machineset
		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get NTO Operator Pod Name
		ntoOperatorPodName := getNTOOperatorPodName(oc, ntoNamespace)

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer deleteMCAndMCPByName(oc, "50-nto-worker-diffcpus", "worker-diffcpus", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 480)
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-bootcmdline-cpu", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineset", "ocp-psap-qe-diffcpus", "-n", "openshift-machine-api", "--ignore-not-found").Execute()

		g.By("create openshift-bootcmdline-cpu tuned profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, nodeDiffCPUsTunedBootFile)

		g.By("create machine config pool")
		applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", nodeDiffCPUsMCPFile, "-p", "MCP_NAME=worker-diffcpus")

		g.By("label the last node with node-role.kubernetes.io/worker-diffcpus=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create a new machineset with different instance type.")
		newMachinesetInstanceType := specifyMachinesetWithDifferentInstanceType(oc)
		e2e.Logf("4 newMachinesetInstanceType is %v, ", newMachinesetInstanceType)
		o.Expect(newMachinesetInstanceType).NotTo(o.BeEmpty())

		createMachinesetbyInstanceType(oc, "ocp-psap-qe-diffcpus", newMachinesetInstanceType)

		g.By("wait for new node is ready when machineset created")
		//1 means replicas=1
		waitForMachinesRunning(oc, 1, "ocp-psap-qe-diffcpus")

		g.By("label the second node with node-role.kubernetes.io/worker-diffcpus=")
		secondTunedNodeName := getNodeNameByMachineset(oc, "ocp-psap-qe-diffcpus")
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondTunedNodeName, "node-role.kubernetes.io/worker-diffcpus-", "--overwrite").Execute()
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondTunedNodeName, "node-role.kubernetes.io/worker-diffcpus=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("assert if the status of adding the two worker node into worker-diffcpus mcp, mcp applied")
		assertIfMCPChangesAppliedByName(oc, "worker-diffcpus", 480)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("assert if openshift-bootcmdline-cpu profile was applied ...")
		//Verify if the new profile is applied
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-bootcmdline-cpu")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("openshift-bootcmdline-cpu"))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		assertNTOPodLogsLastLines(oc, ntoNamespace, ntoOperatorPodName, "25", 180, "Nodes in MCP worker-diffcpus agree on bootcmdline: cpus=")

		//Comment out with an known issue, until it was fixed
		g.By("assert if cmdline was applied in machineconfig...")
		AssertTunedAppliedMC(oc, "nto-worker-diffcpus", "cpus=")

		g.By("assert if cmdline was applied in labled node...")
		o.Expect(AssertTunedAppliedToNode(oc, tunedNodeName, "cpus=")).To(o.Equal(true))

		g.By("<Profiles with bootcmdline conflict> warn message will show in oc get co/node-tuning")
		assertCoStatusWithKeywords(oc, "Profiles with bootcmdline conflict")

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		//Verify if the <Profiles with bootcmdline conflict> warn message disapper after removing custom tuned profile
		g.By("delete openshift-bootcmdline-cpu tuned in labled node...")
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-bootcmdline-cpu", "-n", ntoNamespace, "--ignore-not-found").Execute()

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("removing custom MC and MCP from mcp worker-diffcpus...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-diffcpus-").Execute()

		//remove node from mcp worker-diffcpus
		//To reduce time using delete machineset instead of unlabel secondTunedNodeName node
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("machineset", "ocp-psap-qe-diffcpus", "-n", "openshift-machine-api", "--ignore-not-found").Execute()
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondTunedNodeName, "node-role.kubernetes.io/worker-diffcpus-").Execute()

		g.By("assert if first worker node return to worker mcp")
		assertIfMCPChangesAppliedByName(oc, "worker", 480)

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("<Profiles with bootcmdline conflict> warn message will disappear after removing worker node from mcp worker-diffcpus")
		assertCONodeTuningStatusWithoutWARNWithRetry(oc, 180, "Profiles with bootcmdline conflict")

		g.By("assert if isolcpus was applied in labled node...")
		o.Expect(AssertTunedAppliedToNode(oc, tunedNodeName, "cpus=")).To(o.Equal(false))
	})

	g.It("Author:liqcui-Medium-63223-NTO support tuning sysctl and kernel bools that applied to all nodes of nodepool-level settings in hypershift.", func() {
		//This is a ROSA HCP pre-defined case, only check result, ROSA team will create NTO tuned profile when ROSA HCP created, remove Disruptive
		//Only execute on ROSA hosted cluster
		isROSA := isROSAHostedCluster(oc)
		if !isROSA {
			g.Skip("It's not ROSA hosted cluster - skipping test ...")
		}

		//For ROSA Environment, we are unable to access management cluster, so discussed with ROSA team,
		//ROSA team create pre-defined configmap and applied to specified nodepool with hardcode profile name.
		//NTO will only check if all setting applied to the worker node on hosted cluster.
		g.By("check if the tuned hc-nodepool-vmdratio is created in hosted cluster nodepool")
		tunedNameList, err := oc.AsAdmin().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNameList).NotTo(o.BeEmpty())
		e2e.Logf("The list of tuned tunedNameList is: \n%v", tunedNameList)
		o.Expect(tunedNameList).To(o.And(o.ContainSubstring("hc-nodepool-vmdratio"),
			o.ContainSubstring("tuned-hugepages")))

		appliedProfileList, err := oc.AsAdmin().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(appliedProfileList).NotTo(o.BeEmpty())
		o.Expect(appliedProfileList).To(o.And(o.ContainSubstring("hc-nodepool-vmdratio"),
			o.ContainSubstring("openshift-node-hugepages")))

		g.By("get the node name that applied to the profile hc-nodepool-vmdratio")
		tunedNodeNameStdOut, err := oc.AsAdmin().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace, `-ojsonpath='{.items[?(@..status.tunedProfile=="hc-nodepool-vmdratio")].metadata.name}'`).Output()
		tunedNodeName := strings.Trim(tunedNodeNameStdOut, "'")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())

		g.By("assert the value of sysctl vm.dirty_ratio, the expecte value should be 55")
		debugNodeStdout, err := oc.AsAdmin().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "sysctl", "vm.dirty_ratio").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The value of sysctl vm.dirty_ratio on node %v is: \n%v\n", tunedNodeName, debugNodeStdout)
		o.Expect(debugNodeStdout).To(o.ContainSubstring("vm.dirty_ratio = 55"))

		g.By("get the node name that applied to the profile openshift-node-hugepages")
		tunedNodeNameStdOut, err = oc.AsAdmin().Run("get").Args("profiles.tuned.openshift.io", "-n", ntoNamespace, `-ojsonpath='{.items[?(@..status.tunedProfile=="openshift-node-hugepages")].metadata.name}'`).Output()
		tunedNodeName = strings.Trim(tunedNodeNameStdOut, "'")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())

		g.By("assert the value of cat /proc/cmdline, the expecte value should be hugepagesz=2M hugepages=50")
		debugNodeStdout, err = oc.AsAdmin().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/cmdline").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The value of /proc/cmdline on node %v is: \n%v\n", tunedNodeName, debugNodeStdout)
		o.Expect(debugNodeStdout).To(o.ContainSubstring("hugepagesz=2M hugepages=50"))
	})

	g.It("ROSA-NonHyperShiftHOST-Author:sahshah-Medium-64908-NTO Expose tuned socket interface.[Disruptive]", func() {
		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}

		g.By("pick one worker node to label")
		tunedNodeName, err := getFirstLinuxWorkerNode(oc)
		o.Expect(tunedNodeName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		//Clean up resources
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "-n", ntoNamespace, "tuning-maxpid").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()

		//Label the node with node-role.kubernetes.io/worker-tuning
		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		//Apply new profile that match label node-role.kubernetes.io/worker-tuning=
		g.By("create tuning-maxpid profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, tuningMaxPidFile)

		//NTO will provides two default tuned, one is default
		g.By("check the default tuned list, expected tuning-maxpid")
		allTuneds, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("tuned", "-n", ntoNamespace).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(allTuneds).To(o.ContainSubstring("tuning-maxpid"))

		g.By("check if new profile tuning-maxpid applied to labeled node")
		//Verify if the new profile is applied
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-maxpid")
		profileCheck, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(profileCheck).To(o.Equal("tuning-maxpid"))

		g.By("get current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check the custom profile as expected by debugging the node ")
		printfString := fmt.Sprintf(`printf '{"jsonrpc": "2.0", "method": "active_profile", "id": 1}' | nc -U /run/tuned/tuned.sock`)
		printfStringStdOut, err := remoteShPodWithBash(oc, ntoNamespace, tunedPodName, printfString)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(printfStringStdOut).NotTo(o.BeEmpty())
		o.Expect(printfStringStdOut).To(o.ContainSubstring("tuning-maxpid"))
		e2e.Logf("printfStringStdOut is :\n%v", printfStringStdOut)

	})

	g.It("ROSA-NonHyperShiftHOST-Author:liqcui-Medium-65371-NTO TuneD prevent from reverting node level profiles on termination [Disruptive]", func() {

		// test requires NTO to be installed
		if !isNTO {
			g.Skip("NTO is not installed - skipping test ...")
		}
		//Use the last worker node as labeled node
		var (
			tunedNodeName string
			err           error
		)

		isSNO := isSNOCluster(oc)

		if !isSNO {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
		} else {
			tunedNodeName, err = getFirstLinuxWorkerNode(oc)
			o.Expect(tunedNodeName).NotTo(o.BeEmpty())
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)

		oc.SetupProject()
		ntoTestNS := oc.Namespace()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("namespace", ntoTestNS, "--ignore-not-found").Execute()
		//Clean up resources
		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()

		ntoRes := ntoResource{
			name:        "tuning-pidmax",
			namespace:   ntoNamespace,
			template:    ntoTunedPidMax,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "181818",
		}

		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create tuning-pidmax profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, ntoTunedPidMax)

		g.By("create tuning-pidmax profile tuning-pidmax applied to nodes")
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		AppImageName := getImagestreamImageName(oc, "tests")

		clusterVersion, _, err := getClusterVersion(oc)
		e2e.Logf("Current clusterVersion is [ %v ]", clusterVersion)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(clusterVersion).NotTo(o.BeEmpty())

		g.By("create pod that deletect the value of kernel.pid_max ")
		applyNsResourceFromTemplate(oc, ntoTestNS, "--ignore-unknown-parameters=true", "-f", podSysctlFile, "-p", "IMAGE_NAME="+AppImageName, "RUNASNONROOT=true")

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		//Check if sysctlpod pod is ready
		assertPodToBeReady(oc, "sysctlpod", ntoTestNS)

		g.By("get the sysctlpod status")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoTestNS, "pods").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The status of pod sysctlpod: \n%v", output)

		g.By("check the the value of kernel.pid_max in the pod sysctlpod, the expected value should be kernel.pid_max = 181818")
		podLogStdout, err := oc.AsAdmin().WithoutNamespace().Run("logs").Args("sysctlpod", "--tail=1", "-n", ntoTestNS).Output()
		e2e.Logf("Logs of sysctlpod before delete tuned pod is [ %v ]", podLogStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podLogStdout).NotTo(o.BeEmpty())
		o.Expect(podLogStdout).To(o.ContainSubstring("kernel.pid_max = 181818"))

		g.By("delete tuned pod on the labeled node, and make sure the kernel.pid_max don't revert to origin value")
		o.Expect(oc.AsAdmin().WithoutNamespace().Run("delete").Args("pod", tunedPodName, "-n", ntoNamespace).Execute()).NotTo(o.HaveOccurred())

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check tuned pod status after delete tuned pod")
		//Get the tuned pod name in the same node that labeled node
		tunedPodName = getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		//Check if tuned pod that deleted is ready
		assertPodToBeReady(oc, tunedPodName, ntoNamespace)

		g.By("check the the value of kernel.pid_max in the pod sysctlpod again, the expected value still be kernel.pid_max = 181818")
		podLogStdout, err = oc.AsAdmin().WithoutNamespace().Run("logs").Args("sysctlpod", "--tail=2", "-n", ntoTestNS).Output()
		e2e.Logf("Logs of sysctlpod after delete tuned pod is [ %v ]", podLogStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podLogStdout).NotTo(o.BeEmpty())
		o.Expect(podLogStdout).To(o.ContainSubstring("kernel.pid_max = 181818"))
		o.Expect(podLogStdout).NotTo(o.ContainSubstring("kernel.pid_max not equal 181818"))
	})
	g.It("Longduration-NonPreRelease-PreChkUpgrade-Author:liqcui-Medium-49618-TELCO N-1 - Pre Check for PAO shipped with NTO to support upgrade.[Telco][Disruptive][Slow].", func() {

		var (
			paoBaseProfileMCP = paoFixture("pao-baseprofile-mcp.yaml")
			paoBaseProfile    = paoFixture("pao-baseprofile.yaml")
		)
		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		// currently test is only supported on AWS, GCP, Azure, ibmcloud, alibabacloud
		supportPlatforms := []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

		if !implStringArrayContains(supportPlatforms, iaasPlatform) || isSNO {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test ...")
		}

		totalLinuxWorkerNode := countLinuxWorkerNodeNumByOS(oc)
		totalLinuxWorkerNodes := strconv.Itoa(totalLinuxWorkerNode)
		if totalLinuxWorkerNode < 3 {
			g.Skip("The total linux worker node is " + totalLinuxWorkerNodes + ". The OCP do not have enough worker node, skip it.")
		}

		tunedNodeName := choseOneWorkerNodeToRunCase(oc, 0)

		//Get how many cpus on the specified worker node
		g.By("get the number of cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current cpus cores of worker node is %v", nodeCPUCoresInt)
		if nodeCPUCoresInt < 4 {
			g.Skip("the worker node doesn't have enough cpus - skipping test ...")
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("label the node with node-role.kubernetes.io/worker-pao=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("create machine config pool worker-pao")
		applyOperatorResourceByYaml(oc, "", paoBaseProfileMCP)
		assertIfMCPChangesAppliedByName(oc, "worker-pao", 300)

		// currently test is only supported on AWS, GCP, and Azure
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			//Only GCP and AWS support realtime-kenel
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=true")
		} else {
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=false")
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("assert if machine config pool applied to worker nodes that label with worker-pao")
		assertIfMCPChangesAppliedByName(oc, "worker-pao", 1800)
		assertIfMCPChangesAppliedByName(oc, "worker", 300)
		assertIfMCPChangesAppliedByName(oc, "master", 720)

		g.By("check openshift-node-performance-pao-baseprofile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check current profile openshift-node-performance-pao-baseprofile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile openshift-node-performance-pao-baseprofile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile")

		g.By("check if profile openshift-node-performance-pao-baseprofile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check value of allocatable.hugepages-1Gi in labled node ")
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-1Gi}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeHugePagesOutput).To(o.ContainSubstring("1Gi"))

		g.By("check Settings of CPU Manager policy created by PAO in labled node ")
		cpuManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep cpuManager").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerPolicy"))
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerReconcilePeriod"))
		e2e.Logf("The settings of CPU Manager Policy on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of CPU Manager for reservedSystemCPUs created by PAO in labled node ")
		cpuManagerConfOutput, err = oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep reservedSystemCPUs").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("reservedSystemCPUs"))
		e2e.Logf("The settings of CPU Manager reservedSystemCPUs on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of Topology Manager for topologyManagerPolicy created by PAO in labled node ")
		topologyManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep topologyManagerPolicy").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(topologyManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(topologyManagerConfOutput).To(o.ContainSubstring("topologyManagerPolicy"))
		e2e.Logf("The settings of CPU Manager topologyManagerPolicy on labeled nodes: \n%v", topologyManagerConfOutput)

		// currently test is only supported on AWS, GCP, and Azure
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).To(o.Or(o.ContainSubstring("rt")))
		} else {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).NotTo(o.Or(o.ContainSubstring("rt")))
		}

		g.By("check runtimeClass setting that created by PAO ... ")
		runtimeClassOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile", "pao-baseprofile", "-ojsonpath={.status.runtimeClass}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(runtimeClassOutput).NotTo(o.BeEmpty())
		o.Expect(runtimeClassOutput).To(o.ContainSubstring("performance-pao-baseprofile"))
		e2e.Logf("The settings of runtimeClass on labeled nodes: \n%v", runtimeClassOutput)

		g.By("check Kernel boot settings passed into /proc/cmdline in labled node ")
		kernelCMDLineStdout, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/cmdline").Output()
		e2e.Logf("The settings of Kernel boot passed into /proc/cmdline  on labeled nodes: \n%v", kernelCMDLineStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelCMDLineStdout).NotTo(o.BeEmpty())
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("tsc=reliable"))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("isolcpus="))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("hugepagesz=1G"))

		//o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("nosmt"))
		//     - nosmt  removed nosmt to improve succeed rate due to limited cpu cores
		// but manually renabled when have enough cpu cores

	})

	g.It("Longduration-NonPreRelease-PstChkUpgrade-Author:liqcui-Medium-49618-TELCO N-1 - Post Check for PAO shipped with NTO to support upgrade.[Telco][Disruptive][Slow].", func() {

		if !isNTO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		isSNO := isSNOCluster(oc)

		// currently test is only supported on AWS, GCP, Azure, ibmcloud, alibabacloud
		supportPlatforms := []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

		if !implStringArrayContains(supportPlatforms, iaasPlatform) || isSNO {
			g.Skip("IAAS platform: " + iaasPlatform + " is not automated yet - skipping test ...")
		}

		totalLinuxWorkerNode := countLinuxWorkerNodeNumByOS(oc)
		totalLinuxWorkerNodes := strconv.Itoa(totalLinuxWorkerNode)
		if totalLinuxWorkerNode < 3 {
			g.Skip("The total linux worker node is " + totalLinuxWorkerNodes + ". The OCP do not have enough worker node, skip it.")
		}

		tunedNodeName, _ = oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker-pao", "-ojsonpath={.items[*].metadata.name}").Output()
		if len(tunedNodeName) == 0 {
			g.Skip("No labeled node was found, skipping testing ...")
		} else {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
		}

		defer deleteMCAndMCPByName(oc, "50-nto-worker-pao", "worker-pao", 120)
		defer assertIfMCPChangesAppliedByName(oc, "worker", 600)
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()

		g.By("check If Performance Profile pao-baseprofile and cloud-provider exist during Post Check Phase")
		paoBasePerformanceProfile, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()

		if !strings.Contains(paoBasePerformanceProfile, "pao-baseprofile") {
			g.Skip("No Performancerofile found skipping test ...")
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("assert if machine config pool applied for worker nodes")
		assertIfMCPChangesAppliedByName(oc, "worker-pao", 1200)

		g.By("check openshift-node-performance-pao-baseprofile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check current profile openshift-node-performance-pao-baseprofile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile openshift-node-performance-pao-baseprofile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile")

		g.By("check if profile openshift-node-performance-pao-baseprofile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check value of allocatable.hugepages-1Gi in labled node ")
		nodeHugePagesOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.allocatable.hugepages-1Gi}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeHugePagesOutput).To(o.ContainSubstring("1Gi"))

		g.By("check Settings of CPU Manager policy created by PAO in labled node ")
		cpuManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep cpuManager").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerPolicy"))
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("cpuManagerReconcilePeriod"))
		e2e.Logf("The settings of CPU Manager Policy on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of CPU Manager for reservedSystemCPUs created by PAO in labled node ")
		cpuManagerConfOutput, err = oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep reservedSystemCPUs").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cpuManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(cpuManagerConfOutput).To(o.ContainSubstring("reservedSystemCPUs"))
		e2e.Logf("The settings of CPU Manager reservedSystemCPUs on labeled nodes: \n%v", cpuManagerConfOutput)

		g.By("check Settings of Topology Manager for topologyManagerPolicy created by PAO in labled node ")
		topologyManagerConfOutput, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "/bin/bash", "-c", "cat /etc/kubernetes/kubelet.conf  |grep topologyManagerPolicy").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(topologyManagerConfOutput).NotTo(o.BeEmpty())
		o.Expect(topologyManagerConfOutput).To(o.ContainSubstring("topologyManagerPolicy"))
		e2e.Logf("The settings of CPU Manager topologyManagerPolicy on labeled nodes: \n%v", topologyManagerConfOutput)

		// currently test is only supported on AWS, GCP, and Azure
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).To(o.Or(o.ContainSubstring("rt")))
		} else {
			g.By("check realTime kernel setting that created by PAO in labled node ")
			realTimekernalOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-owide").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(realTimekernalOutput).NotTo(o.BeEmpty())
			o.Expect(realTimekernalOutput).NotTo(o.Or(o.ContainSubstring("rt")))
		}

		g.By("check runtimeClass setting that created by PAO ... ")
		runtimeClassOutput, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile", "pao-baseprofile", "-ojsonpath={.status.runtimeClass}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(runtimeClassOutput).NotTo(o.BeEmpty())
		o.Expect(runtimeClassOutput).To(o.ContainSubstring("performance-pao-baseprofile"))
		e2e.Logf("The settings of runtimeClass on labeled nodes: \n%v", runtimeClassOutput)

		g.By("check Kernel boot settings passed into /proc/cmdline in labled node ")
		kernelCMDLineStdout, err := oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "cat", "/proc/cmdline").Output()
		e2e.Logf("The settings of Kernel boot passed into /proc/cmdline  on labeled nodes: \n%v", kernelCMDLineStdout)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(kernelCMDLineStdout).NotTo(o.BeEmpty())
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("tsc=reliable"))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("isolcpus="))
		o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("hugepagesz=1G"))

		//o.Expect(kernelCMDLineStdout).To(o.ContainSubstring("nosmt"))
		//     - nosmt  removed nosmt to improve succeed rate due to limited cpu cores
		// but manually renabled when have enough cpu cores

		//The custom mc and mcp must be deleted by correct sequence, unlabel first and labeled node return to worker mcp, then delete mc and mcp
		//otherwise the mcp will keep degrade state, it will affected other test case that use mcp
		g.By("delete custom MC and MCP by following correct logic ...")
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
		assertIfMCPChangesAppliedByName(oc, "worker", 600)
		deleteMCAndMCPByName(oc, "50-nto-worker-pao", "worker-pao", 120)
	})

	g.It("NonPreRelease-PreChkUpgrade-Author:liqcui-Medium-21995-Pre Check for basic NTO function to Upgrade OCP Cluster[Disruptive].", func() {

		// currently test is only supported on AWS, GCP, Azure, ibmcloud, alibabacloud
		supportPlatforms := []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

		if !implStringArrayContains(supportPlatforms, iaasPlatform) || !isNTO {
			g.Skip("NTO is not installed or IAAS platform: " + iaasPlatform + " is not automated yet - skipping test ...")
		}

		tunedNodeName := choseOneWorkerNodeToRunCase(oc, 1)

		paoNodeName, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker-pao", "-ojsonpath={.items[*].metadata.name}").Output()
		if len(tunedNodeName) == 0 || tunedNodeName == paoNodeName {
			g.Skip("No suitable worker node was found in : " + iaasPlatform + " - skipping test ...")
		}

		g.By("label the node with node-role.kubernetes.io/worker-tuning=")
		err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning=", "--overwrite").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		ntoRes := ntoResource{
			name:        "tuning-pidmax",
			namespace:   ntoNamespace,
			template:    ntoSysctlTemplate,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "282828",
			label:       "node-role.kubernetes.io/worker-tuning",
		}

		g.By("create tuning-pidmax profile")
		ntoRes.applyNTOTunedProfile(oc)

		g.By("create tuning-pidmax profile tuning-pidmax applied to nodes")
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("compare if the value kernel.pid_max in on labeled node, should be 282828")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.pid_max", "282828")

		g.By("get cloud provider name ...")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("apply cloud-provider profile ...")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cloudProviderFile, "-p", "PROVIDER_NAME="+providerName)

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")
	})

	g.It("NonPreRelease-PstChkUpgrade-Author:liqcui-Medium-21995-Post Check for basic NTO function to Upgrade OCP Cluster[Disruptive]", func() {

		// currently test is only supported on AWS, GCP, Azure, ibmcloud, alibabacloud
		supportPlatforms := []string{"aws", "gcp", "azure", "ibmcloud", "alibabacloud"}

		if !implStringArrayContains(supportPlatforms, iaasPlatform) || !isNTO {
			g.Skip("NTO is not installed or IAAS platform: " + iaasPlatform + " is not automated yet - skipping test ...")
		}

		tunedNodeName, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("nodes", "-l", "node-role.kubernetes.io/worker-tuning", "-ojsonpath={.items[*].metadata.name}").Output()
		if len(tunedNodeName) == 0 {
			g.Skip("No suitable worker node was found in : " + iaasPlatform + " - skipping test ...")
		}

		defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("get cloud provider name ...")
		providerName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("profiles.tuned.openshift.io", tunedNodeName, "-n", ntoNamespace, "-ojsonpath={.spec.config.providerName}").Output()
		o.Expect(providerName).NotTo(o.BeEmpty())
		o.Expect(err).NotTo(o.HaveOccurred())

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace, "--ignore-not-found").Execute()

		ntoRes := ntoResource{
			name:        "tuning-pidmax",
			namespace:   ntoNamespace,
			template:    ntoSysctlTemplate,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "282828",
			label:       "node-role.kubernetes.io/worker-tuning",
		}

		g.By("create tuning-pidmax profile and apply it to nodes")
		ntoRes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "tuning-pidmax", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("compare if the value kernel.pid_max in on labeled node, should be 282828")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.pid_max", "282828")

		providerID, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.spec.providerID}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(providerID).NotTo(o.BeEmpty())
		o.Expect(providerID).To(o.ContainSubstring(providerName))

		g.By("apply cloud-provider profile ...")
		applyNsResourceFromTemplate(oc, ntoNamespace, "--ignore-unknown-parameters=true", "-f", cloudProviderFile, "-p", "PROVIDER_NAME="+providerName)

		g.By("check provider + providerName profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).NotTo(o.BeEmpty())
		o.Expect(tunedNames).To(o.ContainSubstring("provider-" + providerName))

		g.By("check current profile for each node")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check the value of vm.admin_reserve_kbytes on target nodes, the expected value is 16386")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "vm.admin_reserve_kbytes", "16386")

		//Clean nto resource after upgrade
		oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()
		oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "provider-"+providerName, "-n", ntoNamespace, "--ignore-not-found").Execute()

	})

	g.It("Author:liqcui-Medium-74507-NTO openshift-node-performance-uuid have the same priority warning keeps printing[Disruptive]", func() {

		isSNO := isSNOCluster(oc)
		var firstNodeName string
		var secondNodeName string

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			firstNodeName = choseOneWorkerNodeToRunCase(oc, 0)
			secondNodeName = choseOneWorkerNodeToRunCase(oc, 1)
		} else {
			firstNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
			secondNodeName = choseOneWorkerNodeNotByMachineset(oc, 1)
		}

		firstNodeLabel := getNodeListByLabel(oc, "node-role.kubernetes.io/worker-tuning")
		secondNodeLabel := getNodeListByLabel(oc, "node-role.kubernetes.io/worker-priority18")

		if len(firstNodeLabel) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", firstNodeName, "node-role.kubernetes.io/worker-tuning-").Execute()
		}

		if len(secondNodeLabel) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondNodeName, "node-role.kubernetes.io/worker-priority18-").Execute()
		}

		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-pidmax", "-n", ntoNamespace, "--ignore-not-found").Execute()
		defer oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "tuning-dirtyratio", "-n", ntoNamespace, "--ignore-not-found").Execute()

		//Get the tuned pod name in the same node that labeled node
		ntoOperatorPodName := getNTOOperatorPodName(oc, ntoNamespace)
		o.Expect(ntoOperatorPodName).NotTo(o.BeEmpty())

		g.By("pickup two worker nodes to label node to worker-tuning and worker-priority18 ...")

		if len(firstNodeLabel) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", firstNodeName, "node-role.kubernetes.io/worker-tuning=").Execute()
		}

		if len(secondNodeLabel) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", secondNodeName, "node-role.kubernetes.io/worker-priority18=").Execute()
		}

		firstNTORes := ntoResource{
			name:        "tuning-pidmax",
			namespace:   ntoNamespace,
			template:    ntoSysctlTemplate,
			sysctlparm:  "kernel.pid_max",
			sysctlvalue: "282828",
			label:       "node-role.kubernetes.io/worker-tuning",
		}

		secondNTORes := ntoResource{
			name:        "tuning-dirtyratio",
			namespace:   ntoNamespace,
			template:    ntoSysctlTemplate,
			sysctlparm:  "vm.dirty_ratio",
			sysctlvalue: "56",
			label:       "node-role.kubernetes.io/worker-priority18",
		}

		g.By("create tuning-pidmax profile")
		firstNTORes.applyNTOTunedProfile(oc)

		g.By("create tuning-dirtyratio profile")
		secondNTORes.applyNTOTunedProfile(oc)

		g.By("create tuning-pidmax profile and apply it to nodes")
		firstNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, firstNodeName, "tuning-pidmax", "True")

		g.By("create tuning-dirtyratio profile and apply it to nodes")
		secondNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, secondNodeName, "tuning-dirtyratio", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("compare if the value kernel.pid_max in on labeled node, should be 282828")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, firstNodeName, "kernel.pid_max", "282828")

		g.By("compare if the value kernel.pid_max in on labeled node, should be 282828")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, secondNodeName, "vm.dirty_ratio", "56")

		g.By("assert the log contains recommended profile (nf-conntrack-max) matches current configuratio ")
		ntoOperatorPodLogs, _ := oc.AsAdmin().WithoutNamespace().Run("logs").Args("-n", ntoNamespace, ntoOperatorPodName, "--tail=50").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ntoOperatorPodLogs).NotTo(o.BeEmpty())
		o.Expect(ntoOperatorPodLogs).NotTo(o.ContainSubstring("same priority"))

	})

	g.It("Author:liqcui-Longduration-NonPreRelease-Medium-75555-NTO Tuned pod should starts before workload pods on reboot[Disruptive][Slow].", func() {

		var (
			paoBaseProfileMCP = paoFixture("pao-baseprofile-mcp.yaml")
			paoBaseProfile    = paoFixture("pao-baseprofile.yaml")
		)

		// // test requires NTO to be installed
		isSNO := isSNOCluster(oc)
		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		skipPAODeploy := skipDeployPAO(oc)
		if skipPAODeploy {
			e2e.Logf("PAO deployment skipped and continue to execute test case")
		} else {
			g.Skip("PAO installation required; install the Performance Addon Operator before running this test")
		}

		//Prior to choose worker nodes with machineset
		tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)

		//Get how many cpus on the specified worker node
		g.By("get how many cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())
		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node don't have enough cpus - skipping test ...")
		}
		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		//Re-delete mcp,mc, performance and unlabel node, just in case the test case broken before clean up steps
		defer func() {
			assertIfMCPChangesAppliedByName(oc, "worker", 600)
			deleteMCAndMCPByName(oc, "50-nto-worker-pao", "worker-pao", 120)
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("performanceprofile", "pao-baseprofile", "--ignore-not-found").Execute()
		}()

		labeledNode := getNodeListByLabel(oc, "node-role.kubernetes.io/worker-pao")
		if len(labeledNode) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao-").Execute()
			g.By("label the node with node-role.kubernetes.io/worker-pao=")
			err = oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-pao=", "--overwrite").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}

		// currently test is only supported on AWS, GCP, and Azure
		ocpArch, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.nodeInfo.architecture}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if (iaasPlatform == "aws" || iaasPlatform == "gcp") && ocpArch == "amd64" {
			//Only GCP and AWS support realtime-kenel
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=true")
		} else {
			g.By("apply pao-baseprofile performance profile")
			applyClusterResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", paoBaseProfile, "-p", "ISENABLED=false")
		}

		g.By("check Performance Profile pao-baseprofile was created automatically")
		paoBasePerformanceProfile, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("performanceprofile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(paoBasePerformanceProfile).NotTo(o.BeEmpty())
		o.Expect(paoBasePerformanceProfile).To(o.ContainSubstring("pao-baseprofile"))

		g.By("create machine config pool worker-pao")
		applyOperatorResourceByYaml(oc, "", paoBaseProfileMCP)

		g.By("assert if machine config pool applied for worker nodes")
		assertIfMCPChangesAppliedByName(oc, "worker-pao", 1200)

		g.By("check openshift-node-performance-pao-baseprofile tuned profile should be automatically created")
		tunedNames, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(tunedNames).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		g.By("check current profile openshift-node-performance-pao-baseprofile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("check if new NTO profile openshift-node-performance-pao-baseprofile was applied")
		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node-performance-pao-baseprofile")

		g.By("check if profile openshift-node-performance-pao-baseprofile applied on nodes")
		nodeProfileName, err := getTunedProfile(oc, ntoNamespace, tunedNodeName)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeProfileName).To(o.ContainSubstring("openshift-node-performance-pao-baseprofile"))

		//$ systemctl status  ocp-tuned-one-shot.service
		// ocp-tuned-one-shot.service - TuneD service from NTO image
		// ..
		// Active: inactive (dead) since Thu 2024-06-20 14:29:32 UTC; 5min ago
		// notice the tuned in one shot started and finished before kubelet
		//Return an error when the systemctl status ocp-tuned-one-shot.service is inactive, so err for o.Expect as expected.
		g.By("check if end time of ocp-tuned-one-shot.service prior to startup time of kubelet service")

		//supported property name
		// 0.InactiveExitTimestampMonotonic
		// 1.ExecMainStartTimestampMonotonic
		// 2.ActiveEnterTimestampMonotonic
		// 3.StateChangeTimestampMonotonic
		// 4.ActiveExitTimestampMonotonic
		// 5.InactiveEnterTimestampMonotonic
		// 6.ConditionTimestampMonotonic
		// 7.AssertTimestampMonotonic
		inactiveExitTimestampMonotonicOfOCPTunedOneShotService := showSystemctlPropertyValueOfServiceUnitByName(oc, tunedNodeName, ntoNamespace, "ocp-tuned-one-shot.service", "InactiveExitTimestampMonotonic")
		ocpTunedOneShotServiceStatusInactiveExitTimestamp := getSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(inactiveExitTimestampMonotonicOfOCPTunedOneShotService)

		execMainStartTimestampMonotonicOfKubelet := showSystemctlPropertyValueOfServiceUnitByName(oc, tunedNodeName, ntoNamespace, "kubelet.service", "ExecMainStartTimestampMonotonic")
		kubeletServiceStatusExecMainStartTimestamp := getSystemctlServiceUnitTimestampByPropertyNameWithMonotonic(execMainStartTimestampMonotonicOfKubelet)
		e2e.Logf("ocpTunedOneShotServiceStatusInactiveExitTimestamp is: %v, kubeletServiceStatusActiveEnterTimestamp is: %v", ocpTunedOneShotServiceStatusInactiveExitTimestamp, kubeletServiceStatusExecMainStartTimestamp)

		o.Expect(kubeletServiceStatusExecMainStartTimestamp).To(o.BeNumerically(">", ocpTunedOneShotServiceStatusInactiveExitTimestamp))
	})

	g.It("Author:liqcui-Longduration-NonPreRelease-Medium-75435-NTO deferred feature with annotation deferred update[Disruptive]", func() {

		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
		}

		labeledNode := getNodeListByLabel(oc, "deferred-update")

		if len(labeledNode) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-update-").Execute()
		}

		defer func() {
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-update-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		}()

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("pickup one worker nodes to label node to deferred-update ...")

		if len(labeledNode) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-update=").Execute()
		}

		defferedNTORes := ntoResource{
			name:         "deferred-update-profile",
			namespace:    ntoNamespace,
			template:     ntoDefered,
			sysctlparm:   "kernel.shmmni",
			sysctlvalue:  "8192",
			label:        "deferred-update",
			deferedValue: "update",
		}

		g.By("create deferred-update profile")
		defferedNTORes.applyNTOTunedProfileWithDeferredAnnotation(oc)

		g.By("create deferred-update profile and apply it to nodes")
		defferedNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "deferred-update-profile", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		g.By("compare if the value kernel.shmmni in on labeled node, should be 8192")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")

		g.By("path tuned with new value of kernel.shmmni to 10240")
		patchTunedProfile(oc, ntoNamespace, "deferred-update-profile", ntoDeferedUpdatePatch)

		g.By("path the tuned profile with a new value, the new value take effective after node reboot")
		defferedNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "deferred-update-profile", "False")

		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[0].message}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("The TuneD daemon profile is waiting for the next node restart"))

		g.By("reboot the node with updated tuned profile")
		err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, "-it", tunedPodName, "--", "reboot").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		assertIfMCPChangesAppliedByName(oc, "worker", 600)

		g.By("compare if the value kernel.shmmni in on labeled node, should be 10240")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "10240")

		g.By("removed deffered tuned custom profile and unlabel node")
		defferedNTORes.delete(oc)

		g.By("compare if the value kernel.shmmni in on labeled node, it will rollback to 4096")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
	})

	g.It("Author:sahshah-Longduration-NonPreRelease-Medium-75434-NTO deferred feature with annotation deferred -always[Disruptive]", func() {

		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
		}

		labeledNode := getNodeListByLabel(oc, "deferred-always")

		if len(labeledNode) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-always-").Execute()
		}

		defer func() {
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-always-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		}()

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("pickup one worker nodes to label node to deferred-always ..")

		if len(labeledNode) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-always=").Execute()
		}

		defferedNTORes := ntoResource{
			name:         "deferred-always-profile",
			namespace:    ntoNamespace,
			template:     ntoDefered,
			sysctlparm:   "kernel.shmmni",
			sysctlvalue:  "8192",
			label:        "deferred-always",
			deferedValue: "always",
		}

		g.By("create deferred-always profile")
		defferedNTORes.applyNTOTunedProfileWithDeferredAnnotation(oc)

		g.By("create deferred-always profile and apply it to nodes")
		defferedNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-node", "False")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[0].message}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("The TuneD daemon profile is waiting for the next node restart"))

		g.By("compare if the value kernel.shmmni in on labeled node, should be 4096")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")

		g.By("reboot the node with updated tuned profile")
		err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", ntoNamespace, "-it", tunedPodName, "--", "reboot").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		assertIfMCPChangesAppliedByName(oc, "worker", 600)

		g.By("compare if the value kernel.shmmni in on labeled node, should be 8192")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")

		g.By("removed deffered tuned custom profile and unlabel node")
		defferedNTORes.delete(oc)

		g.By("compare if the value kernel.shmmni in on labeled node, it will rollback to 4096")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
	})

	g.It("Author:liqcui-Longduration-NonPreRelease-Medium-77764-NTO - Failure to pull NTO image preventing startup of ocp-tuned-one-shot.service[Disruptive]", func() {

		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		var (
			ntoDisableHttpsMCPFile = ntoFixture("disable-https-mcp.yaml")
			ntoDisableHttpsPPFile  = ntoFixture("disable-https-pp.yaml")
		)

		proxyStdOut, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-ojsonpath={.spec.httpsProxy}").Output()
		e2e.Logf("proxyStdOut is %v", proxyStdOut)
		o.Expect(err).NotTo(o.HaveOccurred())
		if len(proxyStdOut) == 0 {
			g.Skip("No proxy in the cluster - skipping test ...")
		}
		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
		}

		//Get how many cpus on the specified worker node
		g.By("get how many cpus cores on the labeled worker node")
		nodeCPUCores, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("node", tunedNodeName, "-ojsonpath={.status.capacity.cpu}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(nodeCPUCores).NotTo(o.BeEmpty())

		nodeCPUCoresInt, err := strconv.Atoi(nodeCPUCores)
		o.Expect(err).NotTo(o.HaveOccurred())

		if nodeCPUCoresInt <= 1 {
			g.Skip("the worker node don't have enough cpus - skipping test ...")
		}

		labeledNode := getNodeListByLabel(oc, "node-role.kubernetes.io/worker-nohttps")

		defer func() {
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("mcp", "worker-nohttps", "--ignore-not-found").Execute()
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("PerformanceProfile", "performance", "-n", ntoNamespace, "--ignore-not-found").Execute()
		}()

		if len(labeledNode) == 0 {
			defer func() {
				oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-nohttps-").Execute()
				//make sure labeled node return to worker mcp
				assertIfMCPChangesAppliedByName(oc, "worker", 720)
			}()
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("pickup one worker nodes to label node to worker-nohttps ...")

		if len(labeledNode) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-nohttps=").Execute()
		}

		applyOperatorResourceByYaml(oc, ntoNamespace, ntoDisableHttpsMCPFile)

		g.By("remove NTO image on label node")
		stdOut, _ := debugNodeRetryWithOptionsAndChroot(oc, tunedNodeName, []string{"-q"}, "/bin/bash", "-c", ". /var/lib/ocp-tuned/image.env;podman rmi $NTO_IMAGE --force")
		e2e.Logf("removed NTO image is %v", stdOut)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("apply pao performance profile")
		applyOperatorResourceByYaml(oc, ntoNamespace, ntoDisableHttpsPPFile)
		assertIfMCPChangesAppliedByName(oc, "worker-nohttps", 720)

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		//Inactive status mean error in systemctl status ocp-tuned-one-shot.service, that's expected
		g.By("check systemctl status ocp-tuned-one-shot.service, Active: inactive is expected")
		stdOut, _ = oc.AsAdmin().WithoutNamespace().Run("debug").Args("-n", ntoNamespace, "--quiet=true", "node/"+tunedNodeName, "--", "chroot", "/host", "systemctl", "status", "ocp-tuned-one-shot.service").Output()
		o.Expect(stdOut).To(o.ContainSubstring("ocp-tuned-one-shot.service: Deactivated successfully"))

		g.By("check systemctl status kubelet, Active: active (running) is expected")
		stdOut, err = debugNodeRetryWithOptionsAndChroot(oc, tunedNodeName, []string{"-q"}, "systemctl", "status", "kubelet")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(stdOut).To(o.ContainSubstring("Active: active (running)"))

		g.By("remove NTO image on label node and delete tuned pod, the image can pull successfully")
		stdOut, err = debugNodeRetryWithOptionsAndChroot(oc, tunedNodeName, []string{"-q"}, "/bin/bash", "-c", ". /var/lib/ocp-tuned/image.env;podman rmi $NTO_IMAGE --force")
		e2e.Logf("removed NTO image is %v", stdOut)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = oc.AsAdmin().WithoutNamespace().Run("delete").Args("-n", ntoNamespace, "pod", tunedPodName).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		//Get the tuned pod name in the same node that labeled node again
		tunedPodName = getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		assertPodToBeReady(oc, tunedPodName, ntoNamespace)
		podDescOuput, err := oc.AsAdmin().WithoutNamespace().Run("describe").Args("-n", ntoNamespace, "pod", tunedPodName).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(podDescOuput).To(o.ContainSubstring("Successfully pulled image"))
	})

	g.It("Author:sahshah-Longduration-NonPreRelease-Medium-76674-NTO deferred feature with annotation deferred -never[Disruptive]", func() {

		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
		}

		labeledNode := getNodeListByLabel(oc, "deferred-never")

		if len(labeledNode) == 0 {
			defer oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-never-").Execute()
		}

		defer func() {
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "deferred-never-profile", "-n", ntoNamespace, "--ignore-not-found").Execute()
			compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")
		}()

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		g.By("pickup one worker nodes to label node to deferred-never ...")

		if len(labeledNode) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "deferred-never=").Execute()
		}

		defferedNTORes := ntoResource{
			name:         "deferred-never-profile",
			namespace:    ntoNamespace,
			template:     ntoDefered,
			sysctlparm:   "kernel.shmmni",
			sysctlvalue:  "8192",
			label:        "deferred-never",
			deferedValue: "never",
		}

		g.By("compare if the value kernel.shmmni in on labeled node, should be 4096")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "4096")

		g.By("create deferred-never profile")
		defferedNTORes.applyNTOTunedProfileWithDeferredAnnotation(oc)

		g.By("create deferred-never profile and apply it to nodes")
		defferedNTORes.assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "deferred-never-profile", "True")

		g.By("check current profile for each node")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profiles.tuned.openshift.io").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile for each node: \n%v", output)

		output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile.tuned.openshift.io", tunedNodeName, `-ojsonpath='{.status.conditions[0].message}'`).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).NotTo(o.BeEmpty())
		o.Expect(output).To(o.ContainSubstring("TuneD profile applied"))

		g.By("compare if the value kernel.shmmni in on labeled node, should be 8192")
		compareSpecifiedValueByNameOnLabelNodewithRetry(oc, ntoNamespace, tunedNodeName, "kernel.shmmni", "8192")
	})

	g.It("Author:liqcui-Medium-80233-NTO Manage TuneD profiles with the same name and different content[Disruptive]", func() {

		isSNO := isSNOCluster(oc)

		if !isNTO || isSNO {
			g.Skip("NTO is not installed or is Single Node Cluster- skipping test ...")
		}

		var (
			ntoSameProfileDiffContent1 = ntoFixture("nto-same-profile-diff-content1.yaml")
			ntoSameProfileDiffContent2 = ntoFixture("nto-same-profile-diff-content2.yaml")
		)

		machinesetNum := getTotalLinuxMachinesetNum(oc)
		e2e.Logf("len(machinesetName) is %v", machinesetNum)
		if machinesetNum > 1 {
			tunedNodeName = choseOneWorkerNodeToRunCase(oc, 0)
		} else {
			tunedNodeName = choseOneWorkerNodeNotByMachineset(oc, 0)
		}

		labeledNode := getNodeListByLabel(oc, "node-role.kubernetes.io/worker-dup")

		defer func() {
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-dup1", "-n", ntoNamespace, "--ignore-not-found").Execute()
			oc.AsAdmin().WithoutNamespace().Run("delete").Args("tuned", "openshift-profile-dup2", "-n", ntoNamespace, "--ignore-not-found").Execute()
		}()

		if len(labeledNode) == 0 {
			defer func() {
				oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-dup-").Execute()
			}()
		}

		//Get the tuned pod name in the same node that labeled node
		tunedPodName := getTunedPodNamebyNodeName(oc, tunedNodeName, ntoNamespace)
		o.Expect(tunedPodName).NotTo(o.BeEmpty())

		ntoOperatorPod, err := getNTOPodName(oc, ntoNamespace)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("The tuned operator pod name is: \n%v", ntoOperatorPod)

		g.By("pickup one worker nodes to label node to worker-dup ...")
		if len(labeledNode) == 0 {
			oc.AsAdmin().WithoutNamespace().Run("label").Args("node", tunedNodeName, "node-role.kubernetes.io/worker-dup=").Execute()
		}

		applyOperatorResourceByYaml(oc, ntoNamespace, ntoSameProfileDiffContent1)
		applyOperatorResourceByYaml(oc, ntoNamespace, ntoSameProfileDiffContent2)

		assertIfTunedProfileApplied(oc, ntoNamespace, tunedNodeName, "openshift-profile-dup")

		TunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current tuned list: \n%v", TunedStatus)

		ProfileStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "profile").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Current profile of each nodes: \n%v", ProfileStatus)

		//The status show Duplicate TuneD profile \"openshift-profile\" with conflicting content
		customizedTunedStatus, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned/openshift-profile-dup1", "-ojsonpath={.status}").Output()
		e2e.Logf("customizedTunedStatus is: \n%v", customizedTunedStatus)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(customizedTunedStatus).To(o.And(
			o.ContainSubstring("Duplicate TuneD profile"),
			o.ContainSubstring("conflicting content")))

		customizedTunedStatus, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", ntoNamespace, "tuned/openshift-profile-dup2", "-ojsonpath={.status}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(customizedTunedStatus).To(o.And(
			o.ContainSubstring("Duplicate TuneD profile"),
			o.ContainSubstring("conflicting content")))

		assertNTOPodLogsLastLines(oc, ntoNamespace, ntoOperatorPod, "15", 60, "duplicate TuneD profile openshift-profile-dup with conflicting content detected")

	})

})
