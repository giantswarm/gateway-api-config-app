package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v4/pkg/state"
	"github.com/giantswarm/clustertest/v4/pkg/application"
	"github.com/giantswarm/clustertest/v4/pkg/wait"
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

var awsLBControllerBundle *application.Application

func installDependencies() {
	It("should have aws-lb-controller-bundle deployed", func() {
		org := state.GetCluster().Organization
		clusterName := state.GetCluster().Name
		mcName := state.GetFramework().MC().GetClusterName()
		app := application.New(fmt.Sprintf("%s-aws-lb-controller-bundle", clusterName), "aws-lb-controller-bundle").
			WithCatalog("giantswarm").
			WithOrganization(*org).
			WithVersion("latest").
			WithClusterName(clusterName).
			WithInCluster(true).
			WithInstallNamespace(org.GetNamespace()).
			MustWithValues(fmt.Sprintf(awsLBControllerBundleValues, mcName, clusterName, clusterName), nil)

		err := state.GetFramework().MC().DeployApp(state.GetContext(), *app)
		Expect(err).NotTo(HaveOccurred())

		awsLBControllerBundle = app

		Eventually(wait.IsAppDeployed(state.GetContext(), state.GetFramework().MC(), app.InstallName, org.GetNamespace())).
			WithTimeout(10 * time.Minute).
			WithPolling(5 * time.Second).
			Should(BeTrue())
	})
}

func cleanupDependencies() {
	if awsLBControllerBundle != nil {
		err := state.GetFramework().MC().DeleteApp(state.GetContext(), *awsLBControllerBundle)
		Expect(err).NotTo(HaveOccurred())
	}
}
