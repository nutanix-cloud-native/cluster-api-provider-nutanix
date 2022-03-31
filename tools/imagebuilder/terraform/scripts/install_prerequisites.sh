# https://image-builder.sigs.k8s.io/capi/providers/raw.html
sudo apt-get -y update
sudo apt install -y git unzip make python3-pip qemu-kvm libvirt-daemon-system libvirt-clients virtinst cpu-checker libguestfs-tools libosinfo-bin
sudo usermod -a -G kvm ${USER}
sudo chown root:kvm /dev/kvm
# exit and log back in to make the change take place.
exit 0