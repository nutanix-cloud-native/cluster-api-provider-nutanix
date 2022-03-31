git clone https://github.com/kubernetes-sigs/image-builder.git
export PATH=$PATH:~/.local/bin
cd ~/image-builder/images/capi
chmod +x hack/*
make deps-raw
make build-qemu-ubuntu-2004