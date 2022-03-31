sudo apt-get -y update
sudo apt install -y git unzip make python3-pip qemu-kvm libvirt-daemon-system libvirt-clients virtinst cpu-checker libguestfs-tools libosinfo-bin
sudo usermod -a -G kvm ${USER}
sudo chown ${USER}:kvm /dev/kvm
git clone https://github.com/kubernetes-sigs/image-builder.git
cd ~/image-builder/images/capi
export PATH=$PATH:~/.local/bin
chmod +x hack/*