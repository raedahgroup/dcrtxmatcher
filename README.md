
# Dcrtxmatcher server for join split transaction.

Dcrtxmatcher is a coinshuffle++ server which allows decred ticket buyers to create coinjoin transactions in a trustless way, as defined in the coinshuffle++ paper https://crypsys.mmci.uni-saarland.de/projects/FastDC/paper.pdf

Within coinshuffle++, the dicemix protocol is used for the participants to exchange information. Dicemix uses the flint library to solve polynomial to get the roots and the peer's output address. The flint library is a required dependency, and is the method that is suggested by the authors of the coinshuffle++ paper.

There are two options to get it working. The first is download header files and prebuilt libs. This method is quick and easy. The second is building from scratch with source download.

## Download header files and pre-built libs

\> $ mkdir -p ~/go/src/github.com/raedahgroup

\> $ cd ~/go/src/github.com/raedahgroup

\> $ git clone https://github.com/raedahgroup/dcrtxmatcher.git

\> $ cd dcrtxmatcher

\> $ cp libs/usr-local-lib/* /usr/local/lib

\> $ cp libs/usr-local-include/* /usr/local/include

Finish all steps, then continue to the part to *install golang and dcrtxmatcher*

## Build flint from source (suggested)

#### Install software

\> $ sudo apt-get update

\> $ sudo apt-get install automake

\> $ sudo apt-get install yasm

\> $ sudo apt-get install build-essential

#### Download compress libs files and build

\> $ mkdir flint-build

\> $ cd flint-build

Building libraries

#### mpir: http://www.mpir.org

\> $ wget http://www.mpir.org/mpir-3.0.0.tar.bz2 

\> $ tar -jxvf mpir-3.0.0.tar.bz2

\> $ cd mpir-3.0.0

\> $ ./configure && make

\> $ make check

\> $ make install

#### gmp: https://gmplib.org/

\> $ wget https://gmplib.org/download/gmp/gmp-6.1.2.tar.bz2
  
\> $ tar -jxvf gmp-6.1.2.tar

\> cd ../gmp-6.1.2

\> $ ./configure && make

\> $ make check

\> $ make install

#### mpfr: https://www.mpfr.org

\> $ wget https://www.mpfr.org/mpfr-current/mpfr-4.0.1.tar.bz2
\> $ tar -jxvf mpfr-4.0.1.tar.bz2

\> $ cd ../mpfr-4.0.1

\> $ ./configure && make

\> $ make check

\> $ make install

#### flint: http://www.flintlib.org

open Makefile.subdirs, at line 62, replace -Wl,-r with -r 

\> $ wget http://www.flintlib.org/flint-2.5.2.tar.gz

\> $ tar -jxvf flint-2.5.2.tar.gz

\> $ cd ../flint-2.5.2

\> $ sudo ./configure --with-mpir=/usr/local/ --with-mpfr=/usr/local/ --prefix=flint

\> $ make

\> $ make install

\> $ cp flint/lib/* /usr/local/lib

\> $ export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/usr/local/lib

\> $ source ~/.profile

## Install golang and dcrtxmatcher

\> $ apt install golang-go

\> $ apt install go-dep

\> $ export GOPATH=$HOME/go

\> $ export PATH=$PATH:$GOPATH/bin

\> $ source ~/.profile

\> $ mkdir -p ~/go/src/github.com/raedahgroup

\> $ cd ~/go/src/github.com/raedahgroup

\> $ git clone https://github.com/raedahgroup/dcrtxmatcher.git

\> $ dep ensure -v

\> $ go build -v

\> $ ./dcrtxmatcher
