#!/bin/bash
podman run  \
	--user root \
       	--cap-add SYS_ADMIN \
	--device /dev/fuse  \
	--security-opt seccomp=unconfined \
	--security-opt label=disable \
	-it \
	-v ./tests/:/data:Z \
	image-build:test bash
