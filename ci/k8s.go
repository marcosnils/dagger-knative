package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func setupRemoteEngine(ctx context.Context) error {
	kubecfgPath := filepath.Join(homedir.HomeDir(), ".kube", "config")

	kubeCfg := clientcmd.GetConfigFromFileOrDie(kubecfgPath)

	clientConfig, err := clientcmd.BuildConfigFromFlags("", kubecfgPath)
	if err != nil {
		return err
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return err
	}

	// TODO check if the pod is running

	name := "dagger-engine"

	var pod *v1.Pod
	pod, err = clientset.CoreV1().Pods(kubeNamespace).Get(ctx, name, metav1.GetOptions{})

	var perr *kerr.StatusError

	if ok := errors.As(err, &perr); ok && perr.ErrStatus.Reason == metav1.StatusReasonNotFound {
		// if pod doesn't exist, create it
		privileged := true

		pod, err = clientset.CoreV1().Pods(kubeNamespace).Create(ctx, &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:            name,
						Image:           "registry.dagger.io/engine:v0.3.13",
						SecurityContext: &v1.SecurityContext{Privileged: &privileged},
					},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return err
		}

		for {
			pod, err = clientset.CoreV1().Pods(kubeNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			fmt.Println("Waiting for pod to become ready: ", pod.Status.Phase)
			if pod.Status.Phase == v1.PodRunning {
				break
			}

			if pod.Status.Phase != v1.PodRunning && pod.Status.Phase != v1.PodPending {
				return errors.New("error starting dagger engine pod")
			}
			// TODO change this to use client-go listeners instead of loops
			time.Sleep(1 * time.Second)
		}
	}
	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", fmt.Sprintf("kube-pod://%s?context=%s&namespace=%s&container=%s", pod.Name, kubeCfg.CurrentContext, kubeNamespace, pod.Spec.Containers[0].Name))
	return nil
}
