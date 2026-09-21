package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/application"
	"github.com/giantswarm/clustertest/v5/pkg/logger"
	"github.com/giantswarm/clustertest/v5/pkg/wait"
)

const awsLBControllerBundleValues = `
managementCluster:
  name: %s
  namespace: org-giantswarm

clusterName: %s
clusterID: %s

provider: aws

global:
  podSecurityStandards:
    enforced: true

enableServiceMutatorWebhook: false
`

const awsLBControllerBundleName = "aws-lb-controller-bundle"

var awsLBControllerBundle *application.Application

func installDependencies() {
	It("should have aws-lb-controller-bundle deployed", func() {
		org := state.GetCluster().Organization
		clusterName := state.GetCluster().Name
		mcName := state.GetFramework().MC().GetClusterName()

		// Resolve the version here instead of leaving it as "latest". DeployApp and
		// DeleteApp take the Application by value and it is Build() that resolves
		// "latest" against the GitHub API, so the resolved version never reaches the
		// instance kept for teardown and the AfterSuite asks GitHub again, a full
		// suite later, when the CI token has usually expired.
		version, err := application.GetLatestAppVersion(awsLBControllerBundleName)
		Expect(err).NotTo(HaveOccurred())
		logger.Log("Using %s version %s", awsLBControllerBundleName, version)

		app := application.New(fmt.Sprintf("%s-%s", clusterName, awsLBControllerBundleName), awsLBControllerBundleName).
			WithCatalog("giantswarm").
			WithOrganization(*org).
			WithVersion(version).
			WithClusterName(clusterName).
			WithInCluster(true).
			WithInstallNamespace(org.GetNamespace()).
			MustWithValues(fmt.Sprintf(awsLBControllerBundleValues, mcName, clusterName, clusterName), nil)

		Expect(state.GetFramework().MC().DeployApp(state.GetContext(), *app)).To(Succeed())

		awsLBControllerBundle = app

		Eventually(wait.IsAppDeployed(state.GetContext(), state.GetFramework().MC(), app.InstallName, org.GetNamespace())).
			WithTimeout(10 * time.Minute).
			WithPolling(5 * time.Second).
			Should(BeTrue())
	})
}

// cleanupDependencies removes the dependency app again. Best effort: the workload
// cluster and its organization namespace are torn down right after, so a failure
// here must not turn a green suite red.
func cleanupDependencies() {
	if awsLBControllerBundle == nil {
		return
	}

	if err := state.GetFramework().MC().DeleteApp(state.GetContext(), *awsLBControllerBundle); err != nil {
		logger.Log("Failed to delete app %s: %v", awsLBControllerBundle.InstallName, err)
	}
}
