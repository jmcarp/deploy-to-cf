#!/bin/sh

set -e

mv "${HOME}/bin/cli" "${HOME}/bin/cf"
deploy-to-cf
