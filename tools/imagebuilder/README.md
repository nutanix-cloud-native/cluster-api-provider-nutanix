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
public_key_file_path   = ""
private_key_file_path  = ""
</pre>

Then run following command
<pre>
./create_image_build.sh
</pre>

This will create a ubuntu build vm, build the image and copy it to local output directory from remote vm.
You can find the os image in following dir ./terraform/output/ on your local machine

Upload this image into Nutanix Image Service and use it for creating cluster by specifying it in NutanixMachineTemplate under image section.

## To destroy the build vm
Destroy build vm by running following command
<pre>
./delete_image_build_vm.sh
</pre>