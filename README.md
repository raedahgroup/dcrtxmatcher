
# Dcrtxmatcher server for join split transaction.

Dcrtxmatcher refers dicemix and coinshuffle++ to perform coinjoin as in https://crypsys.mmci.uni-saarland.de/projects/FastDC/paper.pdf

Dicemix uses flint libs to solve polynomial to get roots as peer's output address. So we need to build flint libs and dependency.

There are two options to get it works. The first is download header files and prebuilt libs. This method is quick and easy. The second is building from scratch with source download.

## Download header files and pre-built libs

\> $ mkdir -p ~/go/src/github.com/raedahgroup

\> $ cd ~/go/src/github.com/raedahgroup

\> $ git clone https://github.com/raedahgroup/dcrtxmatcher.git

\> $ cd dcrtxmatcher

\> $ cp libs/usr-local-lib/* /usr/local/lib

\> $ cp libs/usr-local-include/* /usr/local/include

All steps finish then continue with part *Install golang and dcrtxmatcher*

## Build flint from scratch (suggestion)

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

\> $ apt install go-dep

\> $ mkdir -p ~/go/src/github.com/raedahgroup

\> $ cd ~/go/src/github.com/raedahgroup

\> $ git clone https://github.com/raedahgroup/dcrtxmatcher.git

\> $ dep ensure -v

\> $ go build -v

\> $ ./dcrtxmatcher
