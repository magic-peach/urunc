# Running urunc with kind (Kubernetes in Docker)

This guide provides a step-by-step process to set up and run the `urunc` runtime with a `kind` (Kubernetes in Docker) cluster on an Ubuntu 22.04 host and  deploy a test NGINX unikernel.

## Prerequisites

- **Host**: Ubuntu 22.04 with `sudo` privileges.
- **Tools**: `docker`, `kind`, and `kubectl` installed.
- **KVM**: Host must support KVM and nested virtualization.


## Overview

The goal is to:
1. Configure a `kind` cluster with KVM access for `urunc`.
2. Install `urunc` and required hypervisors (QEMU, Firecracker, Solo5) inside the `kind` node.
3. Set up `containerd` to use `urunc` as a runtime.
4. Deploy a test NGINX unikernel Pod using the `urunc` runtime.

## Steps

### Step 1: Enable KVM and Nested Virtualization
Ensure the host supports KVM and nested virtualization.

1. **Check KVM support**:

    ```bash
    lsmod | grep kvm
    ```

    If you see `kvm_intel` or `kvm_amd`, KVM is loaded. If not, install it:

    ```bash
    sudo apt update
    sudo apt install -y qemu-kvm
    sudo adduser $USER kvm
    ```

2. **Enable nested virtualization**:

    There are some differences on how to enable nested virtualization depending on your CPU:

    - For Intel:

        ```bash
        echo "options kvm-intel nested=Y" | sudo tee /etc/modprobe.d/kvm-nested.conf
        sudo modprobe -r kvm_intel
        sudo modprobe kvm_intel
        cat /sys/module/kvm_intel/parameters/nested
        ```

        Output should be `Y` or `1`.

    - For AMD:

        ```bash
        echo "options kvm-amd nested=1" | sudo tee /etc/modprobe.d/kvm-nested.conf
        sudo modprobe -r kvm_amd
        sudo modprobe kvm_amd
        cat /sys/module/kvm_amd/parameters/nested
        ```

        Output should be `1`.

3. **Verify KVM**:

    ```bash
    sudo apt install -y cpu-checker
    kvm-ok
    ```

    Output should include "KVM acceleration can be used".

### Step 2: Create a kind Cluster with KVM Access

Configure the `kind` cluster to allow KVM access for running unikernels.

1. **Delete existing cluster (if any)**:
   ```bash
   kind delete cluster --name urunc-test
   ```

2. **Create kind-config.yaml**:
   ```yaml
   kind: Cluster
   apiVersion: kind.x-k8s.io/v1alpha4
   nodes:
     - role: control-plane
       extraMounts:
         - hostPath: /dev/kvm
           containerPath: /dev/kvm
       extraPortMappings:
         - containerPort: 30080   # must match the Service's nodePort in Step 5
           hostPort: 8080
           protocol: TCP
   ```

   The `extraPortMappings` entry forwards a port from the host into the `kind`
   node container. Without it, the NGINX unikernel we deploy later would only
   be reachable from inside the `kind` Docker network, not from the host.

3. **Create the cluster**:
   ```bash
   kind create cluster --name urunc-test --config kind-config.yaml
   ```

4. **Verify KVM access**:
   ```bash
   docker exec urunc-test-control-plane ls /dev/kvm
   ```
   Output should show `/dev/kvm`.

### Step 3: Install urunc and Dependencies in the kind Node

Install `urunc`, hypervisors, and dependencies inside the `kind` node container.

1. **Access the node container**:
   ```bash
   docker exec -it urunc-test-control-plane bash
   ```

2. **Install basic dependencies**:
   ```bash
   apt update
   apt install -y git wget build-essential libseccomp-dev pkg-config bc
   ```

3. **Verify runc (already installed in kind)**:
   ```bash
   which runc
   ```
   If not found, install it:
   ```bash
   RUNC_VERSION=$(curl -s https://api.github.com/repos/opencontainers/runc/releases/latest | grep 'tag_name' | cut -d\" -f4 | sed 's/v//')
   wget -q https://github.com/opencontainers/runc/releases/download/v$RUNC_VERSION/runc.$(dpkg --print-architecture)
   chmod +x runc.$(dpkg --print-architecture)
   mv runc.$(dpkg --print-architecture) /usr/local/sbin/runc
   ```

4. **Configure containerd**:
   Ensure `containerd` is running:
   ```bash
   systemctl status containerd || containerd &
   ```
   Create default config:
   ```bash
   mkdir -p /etc/containerd
   containerd config default > /etc/containerd/config.toml
   ```

5. **Use overlayfs snapshotter**:
   Since `devmapper` is not supported in `kind` containers, use `overlayfs`:
   ```bash
   sed -i 's/snapshotter = "devmapper"/snapshotter = "overlayfs"/' /etc/containerd/config.toml
   ```

6. **Install Go**:
   ```bash
   GO_VERSION=[[ versions.go ]]
   wget -q https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz
   mkdir /usr/local/go${GO_VERSION}
   tar -C /usr/local/go${GO_VERSION} -xzf go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz
   echo "export PATH=\$PATH:/usr/local/go$GO_VERSION/go/bin" >> /etc/profile
   source /etc/profile
   rm -f go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz
   ```

7. **Install urunc**:
   ```bash
   git clone https://github.com/urunc-dev/urunc.git
   cd urunc
   make
   make install
   cd ..
   ```

8. **Install hypervisors**:
   ```bash
   # QEMU
   apt install -y qemu-system

   # Firecracker
   ARCH="$(uname -m)"
   VERSION="v[[ versions.firecracker ]]"
   curl -L https://github.com/firecracker-microvm/firecracker/releases/download/${VERSION}/firecracker-${VERSION}-${ARCH}.tgz | tar -xz
   mv release-${VERSION}-${ARCH}/firecracker-${VERSION}-${ARCH} /usr/local/bin/firecracker
   rm -fr release-${VERSION}-${ARCH}

   # Solo5
   git clone -b v[[ versions.solo5 ]] https://github.com/Solo5/solo5.git
   cd solo5
   ./configure.sh && make -j$(nproc)
   cp tenders/hvt/solo5-hvt /usr/local/bin
   cp tenders/spt/solo5-spt /usr/local/bin
   cd ..
   ```

9. **Add urunc to containerd**:

   Current `containerd` releases (v2.x, including the one shipped in `kind`
   node images) split the old `io.containerd.grpc.v1.cri` plugin into
   separate image and runtime plugins, and runtime handlers now live under
   `io.containerd.cri.v1.runtime` instead. Append the `urunc` runtime entry
   using the current plugin ID:

   ```bash
   cat <<EOF | tee -a /etc/containerd/config.toml
   [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.urunc]
     runtime_type = "io.containerd.urunc.v2"
     container_annotations = ["com.urunc.unikernel.*"]
     pod_annotations = ["com.urunc.unikernel.*"]
     snapshotter = "overlayfs"
   EOF
   systemctl restart containerd || containerd &
   ```

10. **Exit the container**:
    ```bash
    exit
    ```

### Step 4: Create RuntimeClass

Define the `urunc` RuntimeClass for Kubernetes.

1. **Create urunc-runtimeClass.yaml**:
   ```bash
   cat << EOF | tee urunc-runtimeClass.yaml
   kind: RuntimeClass
   apiVersion: node.k8s.io/v1
   metadata:
      name: urunc
   handler: urunc
   EOF
   ```

2. **Apply the RuntimeClass**:
   ```bash
   kubectl apply -f urunc-runtimeClass.yaml
   ```

3. **Verify**:
   ```bash
   kubectl get runtimeClass
   ```

### Step 5: Deploy NGINX Unikernel

1. **Create nginx-urunc.yaml**:
   ```bash
   cat <<EOF | tee nginx-urunc.yaml
   apiVersion: v1
   kind: Pod
   metadata:
     name: nginx-urunc
     labels:
       run: nginx-urunc
   spec:
     runtimeClassName: urunc
     containers:
       - name: nginx
         image: harbor.nbfc.io/nubificus/urunc/nginx-qemu-unikraft-initrd:latest
         imagePullPolicy: Always
         ports:
           - containerPort: 80
             protocol: TCP
         resources:
           requests:
             cpu: 10m
   EOF
   ```

2. **Apply nginx-urunc.yaml**:
   ```bash
   kubectl apply -f nginx-urunc.yaml
   ```

3. **Create nginx-urunc-nodeport.yaml**:

   `kind` nodes run as Docker containers on a private Docker network, so the
   Pod's `ClusterIP` isn't reachable from the host on its own. Expose the Pod
   through a `NodePort` Service, using the port mapped in `kind-config.yaml`:

   ```bash
   cat <<EOF | tee nginx-urunc-nodeport.yaml
   apiVersion: v1
   kind: Service
   metadata:
     name: nginx-urunc-svc
   spec:
     type: NodePort
     selector:
       run: nginx-urunc
     ports:
       - port: 80
         targetPort: 80
         nodePort: 30080   # must match containerPort in kind-config.yaml
   EOF
   ```

4. **Apply nginx-urunc-nodeport.yaml**:
   ```bash
   kubectl apply -f nginx-urunc-nodeport.yaml
   ```

### Step 6: Verify the Deployment

1. **Check Pods**:
   ```bash
   kubectl get pods
   ```
   Look for a Pod named `nginx-urunc-xxxx-yyyy` in `Running` state.

2. **Check Logs**:
   ```bash
   kubectl logs <pod-name>
   ```

   You should see Unikraft logs indicating NGINX startup without errors.

   Output: 
   ```console
   [    1.118355] Info: [libukcpio] <cpio.c @  233> Extracting /./nginx/logs/error.log (0 bytes)
   [    1.150129] Info: [libukcpio] <cpio.c @  286> Creating directory /./nginx/html
   [    1.178385] Info: [libukcpio] <cpio.c @  233> Extracting /./nginx/html/index.html (180 bytes)
   [    1.213847] Info: [libukcpio] <cpio.c @  286> Creating directory /./nginx/conf
   [    1.243744] Info: [libukcpio] <cpio.c @  233> Extracting /./nginx/conf/mime.types (5058 bytes)
   [    1.280254] Info: [libukcpio] <cpio.c @  233> Extracting /./nginx/conf/nginx.conf (361 bytes)
   [    1.319238] Info: [libdevfs] <devfs_vnops.c @  309> Mount devfs to /dev...VFS: mounting devfs at /dev
   Powered by
   o.   .o       _ _               __ _
   Oo   Oo  ___ (_) | __ __  __ _ ' _) :_
   oO   oO ' _ `| | |/ /  _)' _` | |_|  _)
   oOo oOO| | | | |   (| | | (_) |  _) :_
   OoOoO ._, ._:_:_,\_._,  .__,_:_, \___)
         Epimetheus 0.12.0~4c7352c0-custom
   [    1.472947] Info: [libukboot] <boot.c @  348> Pre-init table at 0x29f0d8 - 0x29f0d8
   [    1.509741] Info: [libukboot] <boot.c @  359> Constructor table at 0x29f0d8 - 0x29f0d8
   [    1.547873] Info: [libukboot] <boot.c @  369> Calling main(3, ['/unikernel/app-nginx_kvm-x86_64', '-c', '/nginx/conf/nginx.conf'])
   ```

3. **Send a request to the NGINX unikernel**:

   With the `NodePort` Service and the `kind-config.yaml` port mapping from
   Step 2 in place, the unikernel is reachable from the host at
   `localhost:8080`:

   ```bash
   curl http://localhost:8080
   ```

   You should get back the NGINX unikernel's default HTML response, confirming
   that `urunc` is not just `Running` but actually serving traffic.

### Step 7: Verify urunc Runtime Usage

Confirm that the `nginx-urunc` Pod uses the `urunc` runtime.

1. **Check Pod RuntimeClass**:
   ```bash
   kubectl describe pod <pod-name>
   ```
   Look for:
   ```console
   Runtime Class Name:  urunc
   ```
