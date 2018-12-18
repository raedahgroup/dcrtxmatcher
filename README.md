
# Dcrtxmatcher server for join split transaction.

Dcrtxmatcher is a coinshuffle++ server which allows decred ticket buyers to create coinjoin transactions in a trustless way, as defined in the coinshuffle++ paper https://crypsys.mmci.uni-saarland.de/projects/FastDC/paper.pdf

Within coinshuffle++, the dicemix protocol is used for the participants to exchange information. Dicemix uses the flint library to solve polynomial to get the roots and the peer's output address. The flint library is a required dependency, and is the method that is suggested by the authors of the coinshuffle++ paper.

##  Build instructions for OpenBSD 6.4
```
pkg_add -v yasm
pkg_add -v gmake

mpir: http://www.mpir.org
wget http://www.mpir.org/mpir-3.0.0.tar.bz2
tar -jxvf mpir-3.0.0.tar.bz2
cd mpir-3.0.0
./configure CC=clang
make
make check
make install

gmp: https://gmplib.org/
note, pkg_info -Q gmp  shows gmp-6.1.2p1, but we are staying with install from source.
wget https://gmplib.org/download/gmp/gmp-6.1.2.tar.bz2
tar -jxvf gmp-6.1.2.tar.bz2
cd gmp-6.1.2
./configure
make
make check
make install

mpfr: https://www.mpfr.org
note, pkg_info -Q mpfr  shows mpfr-3.1.5.2, but we are using latest from source.
wget https://www.mpfr.org/mpfr-current/mpfr-4.0.1.tar.bz2
tar -jxvf mpfr-4.0.1.tar.bz2
cd mpfr-4.0.1
./configure --with-gmp-include=../gmp-6.1.2 --with-gmp-lib=/usr/local/lib
make
make check
make install

flint: http://www.flintlib.org
wget http://www.flintlib.org/flint-2.5.2.tar.gz
tar -zxvf flint-2.5.2.tar.gz
cd flint-2.5.2
./configure
gmake
#cp doesnt have a -a on openbsd
changed  285 in Makefile from  -a to -p
gmake install

dcrtxmatcher:
mkdir ~/src
cd ~/src
git clone https://github.com/raedahgroup/dcrtxmatcher.git

#as root
cd /usr/include/
ln -s /usr/local/include/gmp.h
ln -s /usr/local/include/mpfr.h

cd /usr/local/lib
ln -s libflint.so.13.5.2 libflint.so
ln -s libflint.so.13.5.2 libflint.so.13

cd ~/src/dcrtxmatcher
go build -v
```
