#!/bin/sh
# Builds the ImageMagick that Order's proof decoder ships with, into
# /opt/imagemagick. The Dockerfiles and CI all run this script, so tests use
# the same version and delegates as the service. Distro packages are avoided
# because a security update changed how they apply config/imagemagick/policy.xml.
#
# Requires the IMAGEMAGICK_BUILD_DEPS packages from imagemagick-deps.sh.
set -eu

VERSION=7.1.2-34
SHA256=d9d826fec211d56efbb4343f19697d0cfb83b387c1d9ab11cf9c5770056c246c
PREFIX=/opt/imagemagick

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
curl -fsSL -o imagemagick.tar.xz "https://download.imagemagick.org/archive/releases/ImageMagick-$VERSION.tar.xz"
echo "$SHA256  imagemagick.tar.xz" | sha256sum -c -
tar -xJf imagemagick.tar.xz
cd "ImageMagick-$VERSION"
./configure \
  --prefix="$PREFIX" \
  --disable-shared \
  --disable-openmp \
  --without-modules \
  --without-x \
  --without-magick-plus-plus \
  --without-perl \
  --with-heic \
  --with-jpeg \
  --with-png \
  --with-webp
make -j"$(nproc)"
make install
