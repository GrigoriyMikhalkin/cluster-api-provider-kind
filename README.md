## How to run it locally

To run Cluster API with this provider locally:

1. Install `clusterctl`: 
```
curl -L https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.1.3/clusterctl-linux-amd64 -o clusterctl
chmod +x ./clusterctl
sudo mv ./clusterctl /usr/local/bin/clusterctl
```
2. Create `kind` cluster: `kind create cluster`
3. Save kubeconfig from `kind` and export it: `kind get kubeconfig > /tmp/kubeconfig && export KUBECONFIG=/tmp/kubeconfig`
4. Initialize Cluster API controller: `clusterctl init`
5. Install CRDs: `make install`
6. Run provider controller: `make run`
7. Apply sample manifest with cluster definition: `kubectl apply -f ./config/samples/cluster.yaml`