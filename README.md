
# Dcrtxmatcher server for join split transaction.

Dcrtxmatcher refers dicemix and coinshuffle++ to perform coinjoin as in https://crypsys.mmci.uni-saarland.de/projects/FastDC/paper.pdf

Dicemix uses flint libs to solve polynomial to get roots as peer's output address. So we need to build flint libs and dependency.

There are two options to get it works.

## Download header files and pre-built libs

- Download libs directory in dcrtxmatcher.

There are four directories. Copy all files in each directory to equivalent directory of your linux distro.
The paths used in this document is referred to ubuntu 18.4

- usr-local-lib: copy lib files of mpir, mpfr to /usr/local/lib
- usr-local-include: copy header files of mpir, mpfr to /usr/local/include
- usr-local-flint-lib: copy libs of flint to /usr/local/flint/lib
- flint-include: copy header files of flint to dcrtxmatcher/flint/include

## Build flint from scratch (suggestion)

#### Install software

\> $ sudo apt-get update

\> $ sudo apt-get install unzip

\> $ sudo apt-get install lzip

\> $ apt-get install automake

\> $ sudo apt-get install yasm

\> $ sudo apt-get install build-essential

#### Download the following zip files

\> $ mkdir flint-build

\> $ cd flint-build

\> $ wget http://www.mpir.org/mpir-3.0.0.zip 

\> $ wget https://gmplib.org/download/gmp/gmp-6.1.2.tar.lz

\> $ wget https://www.mpfr.org/mpfr-current/mpfr-4.0.1.tar.xz

\> $ wget http://www.flintlib.org/flint-2.5.2.tar.gz

Building libraries

#### mpir: http://www.mpir.org

\> $ unzip mpir-3.0.0.zip

\> $ cd mpir-3.0.0

\> $ ./configure && make

\> $ make check

\> $ make install

#### gmp: https://gmplib.org/

\> $ lzip -d gmp-6.1.2.tar.lz 
  
\> $ tar -xf gmp-6.1.2.tar

\> cd ../gmp-6.1.2

\> $ ./configure && make

\> $ make check

\> $ make install

#### mpfr: https://www.mpfr.org

\> $ cd ../mpfr-4.0.1

\> $ ./configure && make

\> $ make check

\> $ make install

#### flint: http://www.flintlib.org

open Makefile.subdirs, at line 62, replace -Wl,-r with -r 

\> $ cd ../flint-2.5.2

\> $ sudo ./configure --with-mpir=/usr/local/ --with-mpfr=/usr/local/ --prefix=flint

\> $ make

\> $ make install

\> $ cp flint/lib/* /usr/local/lib

\> $ export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/usr/local/lib

\> $ source ~/.profile

Because some include path in flint header files are not correct. I have updated and put on dcrtxmatcher/libs/flint-include.
So when building dcrtxmatcher, just copy all files and directory from dcrtxmatcher/libs/flint-include to dcrtxmatcher/flint/include

\> $ cp -rf dcrtxmatcher/libs/flint-include/* dcrtxmatcher/flint/include


