package operator

import (
	"context"

	kubeinformers "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	imageclient "github.com/openshift/client-go/image/clientset/versioned"
	imageinformers "github.com/openshift/client-go/image/informers/externalversions"
	imageregistryclient "github.com/openshift/client-go/imageregistry/clientset/versioned"
	imageregistryinformers "github.com/openshift/client-go/imageregistry/informers/externalversions"
	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	routeinformers "github.com/openshift/client-go/route/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	"github.com/openshift/cluster-image-registry-operator/pkg/client"
	"github.com/openshift/cluster-image-registry-operator/pkg/defaults"
)

func RunOperator(ctx context.Context, kubeconfig *restclient.Config) error {
	kubeClient, err := kubeclient.NewForConfig(kubeconfig)
	if err != nil {
		return err
	}
	configClient, err := configclient.NewForConfig(kubeconfig)
	if err != nil {
		return err
	}
	imageregistryClient, err := imageregistryclient.NewForConfig(kubeconfig)
	if err != nil {
		return err
	}
	routeClient, err := routeclient.NewForConfig(kubeconfig)
	if err != nil {
		return err
	}
	imageClient, err := imageclient.NewForConfig(kubeconfig)
	if err != nil {
		return err
	}

	kubeInformers := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResyncDuration, kubeinformers.WithNamespace(defaults.ImageRegistryOperatorNamespace))
	kubeInformersForOpenShiftConfig := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResyncDuration, kubeinformers.WithNamespace(defaults.OpenShiftConfigNamespace))
	kubeInformersForOpenShiftConfigManaged := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResyncDuration, kubeinformers.WithNamespace(defaults.OpenShiftConfigManagedNamespace))
	kubeInformersForKubeSystem := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultResyncDuration, kubeinformers.WithNamespace(kubeSystemNamespace))
	configInformers := configinformers.NewSharedInformerFactory(configClient, defaultResyncDuration)
	imageregistryInformers := imageregistryinformers.NewSharedInformerFactory(imageregistryClient, defaultResyncDuration)
	routeInformers := routeinformers.NewSharedInformerFactoryWithOptions(routeClient, defaultResyncDuration, routeinformers.WithNamespace(defaults.ImageRegistryOperatorNamespace))
	imageInformers := imageinformers.NewSharedInformerFactory(imageClient, defaultResyncDuration)

	configOperatorClient := client.NewConfigOperatorClient(
		imageregistryClient.ImageregistryV1().Configs(),
		imageregistryInformers.Imageregistry().V1().Configs(),
	)

	controller := NewController(
		kubeconfig,
		kubeClient,
		configClient,
		imageregistryClient,
		routeClient,
		kubeInformers,
		kubeInformersForOpenShiftConfig,
		kubeInformersForOpenShiftConfigManaged,
		kubeInformersForKubeSystem,
		configInformers,
		imageregistryInformers,
		routeInformers,
	)

	imageConfigStatusController := NewImageConfigController(
		configClient.ConfigV1(),
		configOperatorClient,
		routeInformers.Route().V1().Routes(),
		kubeInformers.Core().V1().Services(),
	)

	clusterOperatorStatusController := NewClusterOperatorStatusController(
		[]configv1.ObjectReference{
			{Group: "imageregistry.operator.openshift.io", Resource: "configs", Name: "cluster"},
			{Group: "imageregistry.operator.openshift.io", Resource: "imagepruners", Name: "cluster"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Name: "system:registry"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Name: "registry-registry-role"},
			{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Name: "openshift-image-registry-pruner"},
			{Resource: "namespaces", Name: defaults.ImageRegistryOperatorNamespace},
		},
		configClient.ConfigV1(),
		configInformers.Config().V1().ClusterOperators(),
		imageregistryInformers.Imageregistry().V1().Configs(),
		imageregistryInformers.Imageregistry().V1().ImagePruners(),
		kubeInformers.Apps().V1().Deployments(),
	)

	imageRegistryCertificatesController := NewImageRegistryCertificatesController(
		kubeClient.CoreV1(),
		configOperatorClient,
		kubeInformers.Core().V1().ConfigMaps(),
		kubeInformers.Core().V1().Services(),
		configInformers.Config().V1().Images(),
		kubeInformersForOpenShiftConfig.Core().V1().ConfigMaps(),
	)

	nodeCADaemonController := NewNodeCADaemonController(
		kubeClient.AppsV1(),
		configOperatorClient,
		kubeInformers.Apps().V1().DaemonSets(),
		kubeInformers.Core().V1().Services(),
	)

	imagePrunerController := NewImagePrunerController(
		kubeClient,
		imageregistryClient,
		kubeInformers,
		imageregistryInformers,
		configInformers.Config().V1().Images(),
	)

	loggingController := loglevel.NewClusterOperatorLoggingController(
		configOperatorClient,
		events.NewLoggingEventRecorder("image-registry"),
	)

	azureStackCloudController := NewAzureStackCloudController(
		configOperatorClient,
		kubeInformersForOpenShiftConfig.Core().V1().ConfigMaps(),
	)

	metricsController := NewMetricsController(imageInformers.Image().V1().ImageStreams())

	kubeInformers.Start(ctx.Done())
	kubeInformersForOpenShiftConfig.Start(ctx.Done())
	kubeInformersForOpenShiftConfigManaged.Start(ctx.Done())
	kubeInformersForKubeSystem.Start(ctx.Done())
	configInformers.Start(ctx.Done())
	imageregistryInformers.Start(ctx.Done())
	routeInformers.Start(ctx.Done())
	imageInformers.Start(ctx.Done())

	go controller.Run(ctx.Done())
	go clusterOperatorStatusController.Run(ctx.Done())
	go nodeCADaemonController.Run(ctx.Done())
	go imageRegistryCertificatesController.Run(ctx.Done())
	go imageConfigStatusController.Run(ctx.Done())
	go imagePrunerController.Run(ctx.Done())
	go loggingController.Run(ctx, 1)
	go azureStackCloudController.Run(ctx)
	go metricsController.Run(ctx)

	<-ctx.Done()
	return nil
}
