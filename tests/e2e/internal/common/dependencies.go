package common

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/giantswarm/apptest-framework/v5/pkg/client"
	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"
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

const (
	awsLBControllerBundleName = "aws-lb-controller-bundle"
	// Pinned: looking up the latest release needs a GitHub token, which fails in CI.
	awsLBControllerBundleVersion = "6.1.0"

	// awsLBControllerBundleServiceAccount is the service account the bundle HelmRelease
	// impersonates. It exists in every organization namespace and is what the framework uses
	// for the gateway-api-bundle HelmRelease too.
	awsLBControllerBundleServiceAccount = "automation"

	awsLBControllerBundleInstallTimeout = 10 * time.Minute
)

var awsLBControllerBundle *client.HelmReleaseConfig

// InstallDependencies registers the spec that installs the apps every suite needs on
// the workload cluster. Call it from the suite's AfterClusterReady.
func InstallDependencies() {
	It("should have aws-lb-controller-bundle deployed", func() {
		org := state.GetCluster().Organization
		clusterName := state.GetCluster().Name
		mcName := state.GetFramework().MC().GetClusterName()

		name := fmt.Sprintf("%s-%s", clusterName, awsLBControllerBundleName)
		cfg := client.HelmReleaseConfig{
			Name:      name,
			Namespace: org.GetNamespace(),
			// The bundle runs in-cluster on the MC, and with kubeConfig unset the
			// flux-multi-tenancy Kyverno policy requires both to match the HelmRelease's namespace.
			TargetNamespace:  org.GetNamespace(),
			StorageNamespace: org.GetNamespace(),
			ChartName:        awsLBControllerBundleName,
			ChartVersion:     awsLBControllerBundleVersion,
			// Named after the HelmRelease so another suite's leftover source can't pin the version.
			SourceName:         name,
			ServiceAccountName: awsLBControllerBundleServiceAccount,
			InCluster:          true,
			Values:             fmt.Sprintf(awsLBControllerBundleValues, mcName, clusterName, clusterName),
		}

		awsLBControllerBundle = &cfg

		ctx, cancel := context.WithTimeout(state.GetContext(), awsLBControllerBundleInstallTimeout)
		defer cancel()
		client.InstallHelmRelease(ctx, cfg)
	})
}

// CleanupDependencies removes the dependency HelmRelease again. Best effort: the workload
// cluster and its organization namespace are torn down right after, so a failure here must not
// turn a green suite red. It does not wait for the uninstall: the suite's AfterSuite runs before
// the framework uninstalls gateway-api-bundle, and the controller has to stay around long enough
// to release the finalizers on the gateway LoadBalancer Services.
func CleanupDependencies() {
	if awsLBControllerBundle == nil {
		return
	}

	cfg := *awsLBControllerBundle
	if err := client.DeleteHelmRelease(state.GetContext(), cfg.Name, cfg.Namespace); err != nil {
		logger.Log("Failed to delete HelmRelease %s/%s: %v", cfg.Namespace, cfg.Name, err)
	}
}
