//go:build e2e
// +build e2e

/*
Copyright 2026 The PodHandoff Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cozyGarage/podhandoff/test/utils"
)

const (
	pilotNamespace = "podhandoff-e2e-pilot"
	canaryLabel    = "podhandoff.io/e2e-canary=true"
)

var _ = Describe("planned-drain pilot", Ordered, func() {
	var canaryNodes []string

	BeforeAll(func() {
		By("waiting for the admission webhook CA bundle")
		Eventually(func(g Gomega) {
			out, err := kubectl("get", "validatingwebhookconfiguration",
				"podhandoff-eviction-hold", "-o", "jsonpath={.webhooks[0].clientConfig.caBundle}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).NotTo(BeEmpty())
		}, 2*time.Minute).Should(Succeed())

		By("reserving two workers that do not host the controller for the canary")
		controllerNode, err := kubectl("get", "pods", "-n", namespace,
			"-l", "control-plane=controller-manager", "-o", "jsonpath={.items[0].spec.nodeName}")
		Expect(err).NotTo(HaveOccurred())
		workers, err := kubectl("get", "nodes", "-l", "!node-role.kubernetes.io/control-plane",
			"-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}")
		Expect(err).NotTo(HaveOccurred())
		for _, node := range utils.GetNonEmptyLines(workers) {
			if node != strings.TrimSpace(controllerNode) {
				canaryNodes = append(canaryNodes, node)
			}
		}
		Expect(canaryNodes).To(HaveLen(2), "the Kind topology must leave two workers outside the controller node")
		for _, node := range canaryNodes {
			_, err = kubectl("label", "node", node, canaryLabel, "--overwrite")
			Expect(err).NotTo(HaveOccurred())
		}

		By("installing a synthetic stateless HTTP canary with meaningful readiness")
		manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: podhandoff-e2e-pilot
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: canary
  namespace: podhandoff-e2e-pilot
spec:
  replicas: 1
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  selector:
    matchLabels:
      app: podhandoff-e2e-canary
  template:
    metadata:
      labels:
        app: podhandoff-e2e-canary
    spec:
      nodeSelector:
        podhandoff.io/e2e-canary: "true"
      containers:
        - name: canary
          image: example.com/podhandoff-e2e-canary:v0.0.1
          imagePullPolicy: Never
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 15
            periodSeconds: 1
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
---
apiVersion: apps.podhandoff.io/v1alpha1
kind: PodHandoff
metadata:
  name: canary
  namespace: podhandoff-e2e-pilot
spec:
  targetRef:
    kind: Deployment
    name: canary
  holdMode: always
  readinessDeadlineSeconds: 90
  minSurgeTimeSeconds: 5
`
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = bytes.NewBufferString(manifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("rollout", "status", "deployment/canary", "-n", pilotNamespace, "--timeout=2m")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			phase, getErr := kubectl("get", "podhandoff", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.status.phase}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(phase)).To(Equal("Idle"))
		}).Should(Succeed())
	})

	AfterAll(func() {
		_, _ = kubectl("delete", "namespace", pilotNamespace, "--ignore-not-found", "--wait=false")
		for _, node := range canaryNodes {
			_, _ = kubectl("uncordon", node)
			_, _ = kubectl("label", "node", node, "podhandoff.io/e2e-canary-")
		}
	})

	It("keeps the old Pod until the replacement is Ready, then evicts and scales back", func() {
		oldPod, err := kubectl("get", "pods", "-n", pilotNamespace,
			"-l", "app=podhandoff-e2e-canary", "-o", "jsonpath={.items[0].metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		oldPod = strings.TrimSpace(oldPod)
		oldNode, err := kubectl("get", "pod", oldPod, "-n", pilotNamespace, "-o", "jsonpath={.spec.nodeName}")
		Expect(err).NotTo(HaveOccurred())
		oldNode = strings.TrimSpace(oldNode)
		var standInPod string

		By("announcing the planned disruption and waiting for an unready stand-in")
		_, err = kubectl("cordon", oldNode)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			replicas, getErr := kubectl("get", "deployment", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.spec.replicas}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(replicas)).To(Equal("2"))
			ready, getErr := kubectl("get", "pods", "-n", pilotNamespace,
				"-l", "app=podhandoff-e2e-canary", "-o",
				"jsonpath={range .items[*]}{.metadata.name}={.status.conditions[?(@.type=='Ready')].status}{'\\n'}{end}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(ready).To(ContainSubstring(oldPod + "=True"))
			for line := range strings.SplitSeq(ready, "\n") {
				name, condition, found := strings.Cut(line, "=")
				if found && name != oldPod && condition == "False" {
					standInPod = name
				}
			}
			g.Expect(standInPod).NotTo(BeEmpty())
		}).Should(Succeed())

		By("starting a real drain and proving eviction remains held before readiness")
		drainDone := make(chan error, 1)
		go func() {
			_, drainErr := kubectl("drain", oldNode, "--ignore-daemonsets", "--delete-emptydir-data",
				"--pod-selector=app=podhandoff-e2e-canary", "--timeout=2m")
			drainDone <- drainErr
		}()
		Consistently(func() error {
			_, getErr := kubectl("get", "pod", oldPod, "-n", pilotNamespace)
			return getErr
		}, 5*time.Second, time.Second).Should(Succeed(), "the old Pod must remain while its stand-in is unready")

		By("waiting for readiness to release eviction")
		Eventually(func(g Gomega) {
			ready, getErr := kubectl("get", "pod", standInPod, "-n", pilotNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(ready)).To(Equal("True"))
		}, 2*time.Minute).Should(Succeed())
		Eventually(drainDone, 2*time.Minute).Should(Receive(BeNil()))

		By("proving the old Pod left and temporary state was scaled back")
		Eventually(func(g Gomega) {
			_, getErr := kubectl("get", "pod", oldPod, "-n", pilotNamespace)
			g.Expect(getErr).To(HaveOccurred())
			replicas, getErr := kubectl("get", "deployment", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.spec.replicas}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(replicas)).To(Equal("1"))
			base, getErr := kubectl("get", "deployment", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.metadata.annotations.podhandoff\\.io/base-replicas}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(base)).To(BeEmpty())
		}).Should(Succeed())
	})

	It("relaxes the eviction hold when the stand-in cannot become Ready", func() {
		oldPod, err := kubectl("get", "pods", "-n", pilotNamespace,
			"-l", "app=podhandoff-e2e-canary", "-o", "jsonpath={.items[0].metadata.name}")
		Expect(err).NotTo(HaveOccurred())
		oldPod = strings.TrimSpace(oldPod)
		oldNode, err := kubectl("get", "pod", oldPod, "-n", pilotNamespace, "-o", "jsonpath={.spec.nodeName}")
		Expect(err).NotTo(HaveOccurred())
		oldNode = strings.TrimSpace(oldNode)

		By("using a short safety deadline and making every eligible replacement node unschedulable")
		_, err = kubectl("patch", "podhandoff", "canary", "-n", pilotNamespace, "--type=merge", "-p",
			`{"spec":{"readinessDeadlineSeconds":15}}`)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("cordon", oldNode)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			replicas, getErr := kubectl("get", "deployment", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.spec.replicas}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(replicas)).To(Equal("2"))
			phase, getErr := kubectl("get", "podhandoff", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.status.phase}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(phase)).To(Equal("Surging"))
			pending, getErr := kubectl("get", "pods", "-n", pilotNamespace,
				"-l", "app=podhandoff-e2e-canary", "--field-selector=status.phase=Pending",
				"-o", "jsonpath={.items[0].metadata.name}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(pending)).NotTo(BeEmpty())
		}).Should(Succeed())

		By("starting a real drain and proving the hold is active before its deadline")
		drainDone := make(chan error, 1)
		go func() {
			_, drainErr := kubectl("drain", oldNode, "--ignore-daemonsets", "--delete-emptydir-data",
				"--pod-selector=app=podhandoff-e2e-canary", "--timeout=2m")
			drainDone <- drainErr
		}()
		Consistently(func() error {
			_, getErr := kubectl("get", "pod", oldPod, "-n", pilotNamespace)
			return getErr
		}, 3*time.Second, time.Second).Should(Succeed(), "the old Pod must remain before the readiness deadline")

		By("observing the deadline escape and successful eviction without a ready stand-in")
		Eventually(func(g Gomega) {
			phase, getErr := kubectl("get", "podhandoff", "canary", "-n", pilotNamespace,
				"-o", "jsonpath={.status.phase}")
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(phase)).To(Equal("Relaxed"))
		}, 30*time.Second).Should(Succeed())
		Eventually(drainDone, 2*time.Minute).Should(Receive(BeNil()))
		Eventually(func() error {
			_, getErr := kubectl("get", "pod", oldPod, "-n", pilotNamespace)
			return getErr
		}).Should(HaveOccurred())
	})
})

func kubectl(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	dir, dirErr := utils.GetProjectDir()
	if dirErr != nil {
		return "", dirErr
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	out := string(output)
	if err != nil {
		return out, fmt.Errorf("kubectl %s: %s: %w", strings.Join(args, " "), out, err)
	}
	return out, nil
}
