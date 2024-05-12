#!/bin/bash

# This script is used to setup a demo cluster on a single machine.

# Function to print message and exit.
function error_exit {
  kubectl delete namespace monitoring 2> /dev/null
  kubectl delete namespace kubescape 2> /dev/null
  kubectl delete namespace falco 2> /dev/null
  echo "$1" 1>&2
  exit 1
}

# Check that kubectl is installed.
if ! [ -x "$(command -v kubectl)" ]; then
  echo "kubectl is not installed. Please install kubectl and try again."
  exit 1
fi

# Check that either miniKube or kind is installed.
if ! [ -x "$(command -v minikube)" ] && ! [ -x "$(command -v kind)" ]; then
  echo "Either minikube or kind is not installed. Please install one of them and try again."
  exit 1
fi

# Check that helm is installed.
if ! [ -x "$(command -v helm)" ]; then
  echo "helm is not installed. Please install helm and try again."
  exit 1
fi

# Add prometheus helm repo and install prometheus.
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts || error_exit "Failed to add prometheus helm repo."
helm repo update || error_exit "Failed to update helm repos."
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
    --namespace monitoring --create-namespace --wait --timeout 5m \
    --set grafana.enabled=true \
    --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false,prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false || error_exit "Failed to install prometheus."

# Check that the prometheus pod is running
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=prometheus -n monitoring --timeout=300s || error_exit "Prometheus did not start."

# Get the absolute path of the directory where this script is located.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
STORAGE_TAG=$($SCRIPT_DIR/storage-tag.sh)
NODE_AGENT_TAG=$($SCRIPT_DIR/node-agent-tag.sh)

# Install node agent chart
helm upgrade --install kubescape $SCRIPT_DIR/../chart --set clusterName=`kubectl config current-context` \
    --set nodeAgent.image.tag=$NODE_AGENT_TAG \
    --set storage.image.tag=$STORAGE_TAG \
    -n kubescape --create-namespace --wait --timeout 5m || error_exit "Failed to install node-agent chart."

# Check that the node-agent pod is running
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=node-agent -n kubescape --timeout=300s || error_exit "Node Agent did not start."

# if WITH_FALCO is set to true, install falco
if [ "$WITH_FALCO" = "true" ]; then
  helm repo add falcosecurity https://falcosecurity.github.io/charts || error_exit "Failed to add falco helm repo."
  helm repo update || error_exit "Failed to update helm repos."
  helm upgrade --create-namespace --install falco -n falco -f falco-demo-values.yaml  falcosecurity/falco --wait --timeout 5m || error_exit "Failed to install falco."
  kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=falco -n falco --timeout=300s || error_exit "Falco did not start."
fi

echo "System test cluster setup complete."


# port forward prometheus
kubectl port-forward svc/alertmanager-operated 9093:9093 -n monitoring &
# Open browser to view alert manager
xdg-open http://localhost:9093
