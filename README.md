# K8s FinOps Operator 🛡️💰

**A Kubernetes Operator designed to enforce budget constraints and optimize resource allocation automatically.**

## 🚀 Overview

The **K8s FinOps Operator** brings financial governance directly into the Kubernetes cluster. It introduces the `ProjectBudget` Custom Resource Definition (CRD), allowing platform engineers to define CPU/Memory limits per namespace (or team).

Unlike standard ResourceQuotas, this operator is **active and intelligent**:

1. **Validating Webhook:** Rejects Pods that exceed the budget.
2. **Mutating Webhook ("Auto-Sizer"):** Automatically resizes Pod requests/limits to fit into the remaining budget if possible, preventing rejection and maximizing resource utilization.
3. **Metrics:** Exposes Prometheus metrics for budget tracking and rejected operations.

## 🛠️ Architecture

The operator follows the Kubernetes Controller pattern and utilizes the `controller-runtime` library.

* **Controller:** Reconciles `ProjectBudget` objects and calculates current usage.
* **Mutating Webhook (`/mutate--v1-pod`):** Intercepts `CREATE` requests. If a Pod requests more CPU than available, but fits within the remainder, it **rewrites the Pod spec** on the fly.
* **Validating Webhook (`/validate--v1-pod`):** The final gatekeeper. If the Pod (original or mutated) still exceeds the budget, the request is **DENIED**.

## ✨ Key Features

* **Namespace-level Budgeting:** Define `MaxCpuLimit` and `MaxMemoryLimit` for specific teams.
* **Intelligent Auto-Resizing:**
* *Scenario:* Budget has 200m left. User requests 400m.
* *Action:* Operator modifies the Pod to 200m automatically.
* *Result:* Pod starts successfully instead of failing.


* **Strict Enforcement:** Blocks deployments that physically cannot fit the budget.
* **Observability:**
* `finops_rejected_pods_total`: Counter of blocked pods.
* `finops_saved_cpu_millicores_total`: Counter of CPU saved by rejection/resizing.



## 📦 Installation

### Prerequisites

* Kubernetes Cluster (v1.25+)
* kubectl
* cert-manager (required for Webhook TLS)

### Quick Start

1. **Clone the repository:**
```sh
git clone https://github.com/YOUR_USERNAME/k8s-governance-operator.git
cd k8s-governance-operator

```


2. **Deploy the Operator:**
```sh
make deploy IMG=acasa93/k8s-finops-operator:v0.11.0

```


3. **Verify Installation:**
```sh
kubectl get pods -n k8s-governance-operator-system

```



## 💡 Usage Example

### 1. Define a Budget

Create a budget for `team-beta` allowing only 500 millicores of CPU.

```yaml
apiVersion: finops.acasa.acme/v1
kind: ProjectBudget
metadata:
  name: beta-budget
spec:
  teamName: team-beta
  maxCpuLimit: "500m"
  validationMode: Enforce

```

### 2. The "Mutating" Magic

Assume 300m are already used. Only **200m** remain.

If you try to deploy a Pod requesting **400m** with the auto-resize annotation:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: hungry-pod
  namespace: team-beta
  annotations:
    finops.acasa.acme/auto-resize: "true"
spec:
  containers:
  - name: nginx
    image: nginx
    resources:
      limits:
        cpu: "400m" # Requesting 400m
      requests:
        cpu: "10m"

```

**Result:** The Pod is created, but `kubectl get pod hungry-pod -o yaml` will show **`limits: cpu: 200m`**. The operator intervened and saved the deployment.

## 📊 Metrics

Prometheus metrics are exposed on port `:8443/metrics`.

```text
# HELP finops_rejected_pods_total Total number of pods rejected by the FinOps operator
# TYPE finops_rejected_pods_total counter
finops_rejected_pods_total{team_namespace="team-beta"} 1

```

## 🛡️ License

Copyright 2026. Distributed under the Apache 2.0 License.
