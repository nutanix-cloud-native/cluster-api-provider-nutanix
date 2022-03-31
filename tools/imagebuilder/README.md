# Steps to create raw OS image for NutanixMachineTemplate

## To create build vm on Nutanix Cloud Infrastructure
Create terraform/terraform.tfvars file with following information and assiging appropriate values
<pre>
endpoint     = ""
password     = ""
user         = ""
image_url    = "https://cloud-images.ubuntu.com/focal/current/focal-server-cloudimg-amd64.img"
cluster_name = ""
vm_name      = "capi_build_vm"
vm_user      = "ubuntu"
subnet_name  = ""
public_key   = ""
</pre>

Then run following command
<pre>
./create_image_build_vm.sh
</pre>

This will create a ubuntu build vm.

## Build the raw OS Image
To build raw OS image, ssh into the above created VM as follows:
<pre>
ssh ubuntu@<insert_build_vm_ip>
</pre>

Per https://image-builder.sigs.k8s.io/capi/providers/raw.html, you can run following commands 
<pre>
sudo apt-get -y update
sudo apt install -y git unzip make python3-pip qemu-kvm libvirt-daemon-system libvirt-clients virtinst cpu-checker libguestfs-tools libosinfo-bin
sudo usermod -a -G kvm ${USER}
sudo chown root:kvm /dev/kvm
# exit and log back in to make the change take place.
exit 0
</pre>

<pre>
git clone https://github.com/kubernetes-sigs/image-builder.git
export PATH=$PATH:~/.local/bin
cd ~/image-builder/images/capi
chmod +x hack/*
make deps-raw
make build-qemu-ubuntu-2004
</pre>

Above will create the respective OS image in ~/image-builder/images/capi/outputs/ubuntu-2004-kube-v1.21.10/ubuntu-2004-kube-v1.21.10

Upload this image into Nutanix Image Service and use it for creating cluster by specifying it in NutanixMachineTemplate under image section.