package common

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/client"
	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/application"
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

	// awsLBControllerBundleServiceAccount is the service account the bundle HelmRelease
	// impersonates. It exists in every organization namespace and is what the framework uses
	// for the gateway-api-bundle HelmRelease too.
	awsLBControllerBundleServiceAccount = "automation"

	awsLBControllerBundleInstallTimeout = 10 * time.Minute
	awsLBControllerBundleCleanupTimeout = 5 * time.Minute
)

var awsLBControllerBundle *client.HelmReleaseConfig

// InstallDependencies registers the spec that installs the apps every suite needs on
// the workload cluster. Call it from the suite's AfterClusterReady.
func InstallDependencies() {
	It("should have aws-lb-controller-bundle deployed", func() {
		org := state.GetCluster().Organization
		clusterName := state.GetCluster().Name
		mcName := state.GetFramework().MC().GetClusterName()

		// Resolve the version here instead of leaving it as "latest": the AfterSuite would
		// otherwise ask GitHub again, a full suite later, when the CI token has usually expired.
		version, err := application.GetLatestAppVersion(awsLBControllerBundleName)
		Expect(err).NotTo(HaveOccurred())
		logger.Log("Using %s version %s", awsLBControllerBundleName, version)

		name := fmt.Sprintf("%s-%s", clusterName, awsLBControllerBundleName)
		cfg := client.HelmReleaseConfig{
			Name:      name,
			Namespace: org.GetNamespace(),
			// The bundle runs in-cluster on the MC, and with kubeConfig unset the
			// flux-multi-tenancy Kyverno policy requires both to match the HelmRelease's namespace.
			TargetNamespace:  org.GetNamespace(),
			StorageNamespace: org.GetNamespace(),
			ChartName:        awsLBControllerBundleName,
			// GitHub release tags carry a "v" prefix, OCI chart tags do not.
			ChartVersion: strings.TrimPrefix(version, "v"),
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
// turn a green suite red. It still waits for the uninstall to reach the workload cluster, since
// the bundle's child HelmRelease can't finalize once the cluster's kubeconfig is gone.
func CleanupDependencies() {
	if awsLBControllerBundle == nil {
		return
	}

	cfg := *awsLBControllerBundle
	ctx, cancel := context.WithTimeout(state.GetContext(), awsLBControllerBundleCleanupTimeout)
	defer cancel()

	if err := client.DeleteHelmRelease(ctx, cfg.Name, cfg.Namespace); err != nil {
		logger.Log("Failed to delete HelmRelease %s/%s: %v", cfg.Namespace, cfg.Name, err)
		return
	}
	if err := client.WaitForHelmReleaseDeleted(ctx, cfg.Name, cfg.Namespace); err != nil {
		logger.Log("Gave up waiting for HelmRelease %s/%s to be deleted: %v", cfg.Namespace, cfg.Name, err)
	}
	if err := client.WaitForHelmReleaseChildrenDeleted(ctx, cfg.Name, cfg.Namespace); err != nil {
		logger.Log("Gave up waiting for the children of HelmRelease %s/%s to be deleted: %v", cfg.Namespace, cfg.Name, err)
	}
	if err := client.DeleteHelmSource(state.GetContext(), cfg); err != nil {
		logger.Log("Failed to delete the source of HelmRelease %s/%s: %v", cfg.Namespace, cfg.Name, err)
	}
}
