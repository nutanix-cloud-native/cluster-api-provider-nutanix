git clone https://github.com/deepakm-ntnx/image-builder.git
export PATH=$PATH:~/.local/bin
cd ~/image-builder/images/capi
git checkout nutanix-imagebuilder

cp ~/nutanix.json packer/nutanix/nutanix.json

chmod +x hack/*
make deps-nutanix
make build-nutanix-ubuntu-2004