# Debian/Ubuntu packages for scripts/build-imagemagick.sh, sourced by the
# Dockerfiles and CI so they install the same delegates.
IMAGEMAGICK_BUILD_DEPS="build-essential ca-certificates curl pkg-config xz-utils \
  libde265-dev libheif-dev libheif-plugin-libde265 libjpeg-dev libpng-dev libwebp-dev"
# The HEVC decoder is a libheif runtime plugin, so it is needed at runtime too.
IMAGEMAGICK_RUNTIME_DEPS="libheif1 libheif-plugin-libde265 libjpeg62-turbo libpng16-16t64 \
  libsharpyuv0 libwebp7 libwebpdemux2 libwebpmux3"
