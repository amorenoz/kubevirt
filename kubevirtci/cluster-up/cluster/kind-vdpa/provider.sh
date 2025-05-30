#!/usr/bin/env bash

set -e

DEFAULT_CLUSTER_NAME="sriov"
DEFAULT_HOST_PORT=5000
ALTERNATE_HOST_PORT=5001
export CLUSTER_NAME=${CLUSTER_NAME:-$DEFAULT_CLUSTER_NAME}

if [ $CLUSTER_NAME == $DEFAULT_CLUSTER_NAME ]; then
    export HOST_PORT=$DEFAULT_HOST_PORT
else
    export HOST_PORT=$ALTERNATE_HOST_PORT
fi

function calculate_mtu() {
    overlay_overhead=58
    current_mtu=$(cat /sys/class/net/$(ip route | grep "default via" | head -1 | awk '{print $5}')/mtu)
    expr $current_mtu - $overlay_overhead
}

MTU=${MTU:-$(calculate_mtu)}

function print_available_nics() {
    # print hardware info for easier debugging based on logs
    echo 'Available NICs'
    ${CRI_BIN} run --rm --cap-add=SYS_RAWIO quay.io/phoracek/lspci@sha256:0f3cacf7098202ef284308c64e3fc0ba441871a846022bb87d65ff130c79adb1 sh -c "lspci | egrep -i 'network|ethernet'"
    echo ""
}

function set_kind_params() {
    version=$(cat "${KUBEVIRTCI_PATH}/cluster/$KUBEVIRT_PROVIDER/version")
    export KIND_VERSION="${KIND_VERSION:-$version}"

    image=$(cat "${KUBEVIRTCI_PATH}/cluster/$KUBEVIRT_PROVIDER/image")
    export KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-$image}"
    export KUBECTL_PATH="${KUBECTL_PATH:-/bin/kubectl}"
}

function print_sriov_data() {
    nodes=$(_kubectl get nodes -o=custom-columns=:.metadata.name | awk NF)
    for node in $nodes; do
        if [[ ! "$node" =~ .*"control-plane".* ]]; then
            echo "Node: $node"
            echo "VFs:"
            ${CRI_BIN} exec $node bash -c "ls -l /sys/class/net/*/device/virtfn*"
            echo "PFs PCI Addresses:"
            ${CRI_BIN} exec $node bash -c "grep PCI_SLOT_NAME /sys/class/net/*/device/uevent"
        fi
    done
}

function configure_registry_proxy() {
    [ "$CI" != "true" ] && return

    echo "Configuring cluster nodes to work with CI mirror-proxy..."

    local -r ci_proxy_hostname="docker-mirror-proxy.kubevirt-prow.svc"
    local -r kind_binary_path="${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/.kind"
    local -r configure_registry_proxy_script="${KUBEVIRTCI_PATH}/cluster/kind/configure-registry-proxy.sh"

    KIND_BIN="$kind_binary_path" PROXY_HOSTNAME="$ci_proxy_hostname" $configure_registry_proxy_script
}

function deploy_sriov() {
    print_available_nics
    ${KUBEVIRTCI_PATH}/cluster/$KUBEVIRT_PROVIDER/config_sriov_cluster.sh
    print_sriov_data
}

export OVNK_COMMIT=c77ee8c38c6a6d9e55131a1272db5fad5b606e44

OVNK_REPO='https://github.com/ovn-org/ovn-kubernetes.git'
CLUSTER_PATH=${CLUSTER_PATH:-"${KUBEVIRTCI_CONFIG_PATH}/${KUBEVIRT_PROVIDER}/_ovnk"}

function cluster::_get_repo() {
    git --git-dir ${CLUSTER_PATH}/.git config --get remote.origin.url
}

function cluster::_get_sha() {
    git --git-dir ${CLUSTER_PATH}/.git rev-parse HEAD
}

function cluster::install() {
    if [ -d ${CLUSTER_PATH} ]; then
        if [ $(cluster::_get_repo) != ${OVNK_REPO} -o $(cluster::_get_sha) != ${OVNK_COMMIT} ]; then
            rm -rf ${CLUSTER_PATH}
        fi
    fi

    if [ ! -d ${CLUSTER_PATH} ]; then
        git clone ${OVNK_REPO} ${CLUSTER_PATH}
        (
            cd ${CLUSTER_PATH}
            git checkout ${OVNK_COMMIT}
        )
    fi
}

function prepare_config_ovn() {
    echo "STEP: Prepare provider config"
    cat >$KUBEVIRTCI_CONFIG_PATH/$KUBEVIRT_PROVIDER/config-provider-$KUBEVIRT_PROVIDER.sh <<EOF
master_ip=127.0.0.1
kubeconfig=${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/.kubeconfig
kubectl=kubectl
docker_prefix=127.0.0.1:5000/kubevirt
manifest_docker_prefix=localhost:5000/kubevirt
EOF
}

function fetch_kind() {
    _fetch_kind
    KIND="${KUBEVIRTCI_CONFIG_PATH}"/"$KUBEVIRT_PROVIDER"/.kind
    export KIND_PATH="${KUBEVIRTCI_CONFIG_PATH}"/"$KUBEVIRT_PROVIDER"
    export PATH=$KIND_PATH:$PATH
    ln -s $KIND_PATH/kind $KIND_PATH/.kind
}

function setup_kubectl() {
    if ${CRI_BIN} exec ${CLUSTER_NAME}-control-plane ls /usr/bin/kubectl > /dev/null; then
        kubectl_path=/usr/bin/kubectl
    elif ${CRI_BIN} exec ${CLUSTER_NAME}-control-plane ls /bin/kubectl > /dev/null; then
        kubectl_path=/bin/kubectl
    else
        echo "Error: kubectl not found on node, exiting"
        exit 1
    fi

    ${CRI_BIN} cp ${CLUSTER_NAME}-control-plane:$kubectl_path ${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/.kubectl

    chmod u+x ${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/.kubectl
}

function up() {
    export CONFIG_WORKER_CPU_MANAGER=true
    export CONFIG_TOPOLOGY_MANAGER_POLICY="single-numa-node"
    cluster::install
    pushd $CLUSTER_PATH/contrib ; ./kind.sh --cluster-name $CLUSTER_NAME --multi-network-enable -mtu $MTU --local-kind-registry --enable-interconnect --num-workers $((KUBEVIRT_NUM_NODES - 1)); popd
    cp ~/$CLUSTER_NAME.conf "${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/.kubeconfig"
    cp ${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/_ovnk/contrib/kind-${CLUSTER_NAME}.yaml ${KUBEVIRTCI_CONFIG_PATH}/$KUBEVIRT_PROVIDER/kind.yaml
    setup_kubectl
    prepare_config_ovn
    _fix_node_labels

    configure_registry_proxy

    # remove the rancher.io kind default storageClass
    _kubectl delete sc standard

    deploy_sriov
    echo "$KUBEVIRT_PROVIDER cluster '$CLUSTER_NAME' is ready"
}

set_kind_params

source ${KUBEVIRTCI_PATH}/cluster/kind/common.sh
