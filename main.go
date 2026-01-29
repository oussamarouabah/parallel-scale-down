package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

var (
	inputFilePath string
	rootCmd       = &cobra.Command{
		Use:          "parallel-scale-down",
		Short:        "Scale down deployments and statefulsets in parallel",
		SilenceUsage: true,
		RunE:         run,
	}
)

func init() {
	rootCmd.Flags().StringVar(&inputFilePath, "file", "", "Path to the input yaml file containing list of deployments and statefulsets")
	_ = rootCmd.MarkFlagRequired("file")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	config, err := readConfigFile(inputFilePath)
	if err != nil {
		return fmt.Errorf("error reading config file: %v", err)
	}

	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return fmt.Errorf("error building kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return fmt.Errorf("error creating clientset: %v", err)
	}

	return runScaleDown(cmd.Context(), clientset, config)
}

type Config struct {
	Deployments  []ResourceItem `yaml:"deployments"`
	StatefulSets []ResourceItem `yaml:"statefulsets"`
}

type ResourceItem struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Replicas  *int32 `yaml:"replicas"`
}

func readConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runScaleDown(ctx context.Context, clientset *kubernetes.Clientset, config *Config) error {
	var wg sync.WaitGroup
	totalOps := len(config.Deployments) + len(config.StatefulSets)
	errChan := make(chan error, totalOps)

	fmt.Println("Starting parallel scale down...")

	for _, d := range config.Deployments {
		wg.Add(1)
		go func(r ResourceItem) {
			defer wg.Done()
			if err := scaleDownAndWatch(ctx, clientset, r, "deployment"); err != nil {
				errChan <- fmt.Errorf("Deployment %s/%s: %v", r.Namespace, r.Name, err)
			}
		}(d)
	}

	for _, s := range config.StatefulSets {
		wg.Add(1)
		go func(r ResourceItem) {
			defer wg.Done()
			if err := scaleDownAndWatch(ctx, clientset, r, "statefulset"); err != nil {
				errChan <- fmt.Errorf("StatefulSet %s/%s: %v", r.Namespace, r.Name, err)
			}
		}(s)
	}

	wg.Wait()
	close(errChan)

	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		fmt.Println("\n---------------------------------------------------")
		fmt.Println("The following resources failed to scale down:")
		for _, err := range errors {
			fmt.Printf("- %v\n", err)
		}
		fmt.Println("---------------------------------------------------")
		return fmt.Errorf("finished with %d errors", len(errors))
	}

	fmt.Println("\n---------------------------------------------------")
	fmt.Println("All deployments and statefulsets are scaled down to target.")
	fmt.Println("Ready to start the maintenance.")
	fmt.Println("---------------------------------------------------")
	return nil
}

func scaleDownAndWatch(ctx context.Context, clientset *kubernetes.Clientset, r ResourceItem, kind string) error {
	fmt.Printf("[%s/%s] Starting scale down...\n", r.Namespace, r.Name)

	switch kind {
	case "deployment":
		return handleDeployment(ctx, clientset, r)
	case "statefulset":
		return handleStatefulSet(ctx, clientset, r)
	default:
		return fmt.Errorf("unsupported kind: %s", kind)
	}
}

func getTargetReplicas(r ResourceItem) int32 {
	if r.Replicas == nil {
		return 0
	}
	return *r.Replicas
}

func handleDeployment(ctx context.Context, clientset *kubernetes.Clientset, r ResourceItem) error {
	deploymentsClient := clientset.AppsV1().Deployments(r.Namespace)
	targetReplicas := getTargetReplicas(r)

	var watch = true

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, err := deploymentsClient.Get(ctx, r.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if *d.Spec.Replicas == targetReplicas {
			fmt.Printf("[%s/%s] Already at %d replicas.\n", r.Namespace, r.Name, targetReplicas)
			watch = false
			return nil
		}

		d.Spec.Replicas = &targetReplicas
		_, err = deploymentsClient.Update(ctx, d, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return err
	}

	if !watch {
		return nil
	}

	fmt.Printf("[%s/%s] Scaled down command sent. Watching for %d replicas...\n", r.Namespace, r.Name, targetReplicas)
	return waitForDeploymentScaleDown(ctx, clientset, r, targetReplicas)
}

func handleStatefulSet(ctx context.Context, clientset *kubernetes.Clientset, r ResourceItem) error {
	stsClient := clientset.AppsV1().StatefulSets(r.Namespace)
	targetReplicas := getTargetReplicas(r)

	var watch = true

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		s, err := stsClient.Get(ctx, r.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if *s.Spec.Replicas == targetReplicas {
			fmt.Printf("[%s/%s] Already at %d replicas.\n", r.Namespace, r.Name, targetReplicas)
			watch = false
			return nil
		}

		s.Spec.Replicas = &targetReplicas
		_, err = stsClient.Update(ctx, s, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return err
	}

	if !watch {
		return nil
	}

	fmt.Printf("[%s/%s] Scaled down command sent. Watching for %d replicas...\n", r.Namespace, r.Name, targetReplicas)

	return waitForStatefulSetScaleDown(ctx, clientset, r, targetReplicas)
}

func waitForDeploymentScaleDown(ctx context.Context, clientset *kubernetes.Clientset, r ResourceItem, targetReplicas int32) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		d, err := clientset.AppsV1().Deployments(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if d.Status.Replicas == targetReplicas {
			fmt.Printf("[%s/%s] Scale complete.\n", r.Namespace, r.Name)
			break
		}
		fmt.Printf("[%s/%s] Waiting for deployment scale down... Current replicas: %d\n", r.Namespace, r.Name, d.Status.Replicas)
	}

	return nil
}

func waitForStatefulSetScaleDown(ctx context.Context, clientset *kubernetes.Clientset, r ResourceItem, targetReplicas int32) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s, err := clientset.AppsV1().StatefulSets(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		if s.Status.Replicas == targetReplicas {
			fmt.Printf("[%s/%s] Scale complete.\n", r.Namespace, r.Name)
			break
		}

		fmt.Printf("[%s/%s] Waiting for statefulset scale down... Current replicas: %d\n", r.Namespace, r.Name, s.Status.Replicas)
	}

	return nil
}
