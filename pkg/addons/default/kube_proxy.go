package defaultaddons

import (
	"context"
	"fmt"
	"strings"

	v1 "k8s.io/api/apps/v1"

	"github.com/kris-nova/logger"
	"github.com/pkg/errors"

	"github.com/weaveworks/eksctl/pkg/addons"
	"github.com/weaveworks/eksctl/pkg/printers"

	api "github.com/weaveworks/eksctl/pkg/apis/eksctl.io/v1alpha5"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/weaveworks/eksctl/pkg/utils"
	"k8s.io/client-go/kubernetes"
)

const (
	// KubeProxy is the name of the kube-proxy addon
	KubeProxy     = "kube-proxy"
	ArchBetaLabel = "beta.kubernetes.io/arch"
	ArchLabel     = "kubernetes.io/arch"
)

func IsKubeProxyUpToDate(clientSet kubernetes.Interface, controlPlaneVersion string) (bool, error) {
	d, err := clientSet.AppsV1().DaemonSets(metav1.NamespaceSystem).Get(context.TODO(), KubeProxy, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Warning("%q was not found", KubeProxy)
			return true, nil
		}
		return false, errors.Wrapf(err, "getting %q", KubeProxy)
	}
	if numContainers := len(d.Spec.Template.Spec.Containers); !(numContainers >= 1) {
		return false, fmt.Errorf("%s has %d containers, expected at least 1", KubeProxy, numContainers)
	}

	desiredTag, err := kubeProxyImageTag(controlPlaneVersion)
	if err != nil {
		return false, err
	}
	image := d.Spec.Template.Spec.Containers[0].Image
	imageTag, err := addons.ImageTag(image)
	if err != nil {
		return false, err
	}
	return desiredTag == imageTag, nil
}

// UpdateKubeProxy updates image tag for kube-system:daemonset/kube-proxy based to match controlPlaneVersion
func UpdateKubeProxy(clientSet kubernetes.Interface, controlPlaneVersion string, plan bool) (bool, error) {
	printer := printers.NewJSONPrinter()

	d, err := clientSet.AppsV1().DaemonSets(metav1.NamespaceSystem).Get(context.TODO(), KubeProxy, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			logger.Warning("%q was not found", KubeProxy)
			return false, nil
		}
		return false, errors.Wrapf(err, "getting %q", KubeProxy)
	}

	archLabel := ArchLabel
	isMinVersion, err := utils.IsMinVersion(api.Version1_18, controlPlaneVersion)
	if err != nil {
		return false, err
	}
	if !isMinVersion {
		archLabel = ArchBetaLabel
	}

	hasArm64NodeSelector := daemeonSetHasArm64NodeSelector(d, archLabel)
	if !hasArm64NodeSelector {
		logger.Info("missing arm64 nodeSelector value")
	}

	if numContainers := len(d.Spec.Template.Spec.Containers); !(numContainers >= 1) {
		return false, fmt.Errorf("%s has %d containers, expected at least 1", KubeProxy, numContainers)
	}

	if err := printer.LogObj(logger.Debug, KubeProxy+" [current] = \\\n%s\n", d); err != nil {
		return false, err
	}

	image := &d.Spec.Template.Spec.Containers[0].Image
	imageParts := strings.Split(*image, ":")

	if len(imageParts) != 2 {
		return false, fmt.Errorf("unexpected image format %q for %q", *image, KubeProxy)
	}

	desiredTag, err := kubeProxyImageTag(controlPlaneVersion)
	if err != nil {
		return false, err
	}

	if imageParts[1] == desiredTag && hasArm64NodeSelector {
		logger.Debug("imageParts = %v, desiredTag = %s", imageParts, desiredTag)
		logger.Info("%q is already up-to-date", KubeProxy)
		return false, nil
	}

	if plan {
		logger.Critical("(plan) %q is not up-to-date", KubeProxy)
		return true, nil
	}

	imageParts[1] = desiredTag
	*image = strings.Join(imageParts, ":")

	if err := printer.LogObj(logger.Debug, KubeProxy+" [updated] = \\\n%s\n", d); err != nil {
		return false, err
	}

	if !hasArm64NodeSelector {
		addArm64NodeSelector(d, archLabel)
	}

	if _, err := clientSet.AppsV1().DaemonSets(metav1.NamespaceSystem).Update(context.TODO(), d, metav1.UpdateOptions{}); err != nil {
		return false, err
	}

	logger.Info("%q is now up-to-date", KubeProxy)
	return false, nil
}

func daemeonSetHasArm64NodeSelector(daemonSet *v1.DaemonSet, archLabel string) bool {
	if daemonSet.Spec.Template.Spec.Affinity != nil &&
		daemonSet.Spec.Template.Spec.Affinity.NodeAffinity != nil &&
		daemonSet.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		for _, nodeSelectorTerms := range daemonSet.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
			for _, nodeSelector := range nodeSelectorTerms.MatchExpressions {
				if nodeSelector.Key == archLabel {
					for _, value := range nodeSelector.Values {
						if value == "arm64" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func addArm64NodeSelector(daemonSet *v1.DaemonSet, archLabel string) {
	for nodeSelectorTermsIndex, nodeSelectorTerms := range daemonSet.Spec.Template.Spec.Affinity.NodeAffinity.
		RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for nodeSelectorIndex, nodeSelector := range nodeSelectorTerms.MatchExpressions {
			if nodeSelector.Key == archLabel {
				daemonSet.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.
					NodeSelectorTerms[nodeSelectorTermsIndex].MatchExpressions[nodeSelectorIndex].Values = append(nodeSelector.Values, "arm64")
			}
		}
	}
}

func kubeProxyImageTag(controlPlaneVersion string) (string, error) {
	return fmt.Sprintf("v%s-eksbuild.1", controlPlaneVersion), nil
}
