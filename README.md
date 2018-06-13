# Advanced Copy (ACP)

### Multi-buffer copy to minimise fragmentation and maximize throughput

The `acp` tool runs multithreaded in order to read and write at the same time. It will not read multiple files at once, but will ensure that a read is happening while a write is happening. This ensures that multi-location copies are handled faster - as there is no need to wait for write to flush before the next buffered read, and no need to wait for the read to complete before writing the next buffer.

Two job threads exist, one for reading and one for writing. When thread one is done with a file, it will read the next file already as well, while thread 2 is finishing it's write to the first file, and so forth.

The tools also allows to specify non-standard buffer size. With larger buffers, there will be much less fragmentation.

Copying dirs and files, link preservation(and not), concat multiple files into one file and other features. See help below.

Note that you need buffer-size X 2 (times two) of RAM, as for threaded copy we use 2 buffers. So if buffer-size is 1GB to minimize a very large file fragmentation, you need 2GB of RAM to run the `acp`

### Where will multithreading help

Obviously, it won't help if you are copying a file or files on a laptop on a local disk from one directory to another.

It will help if you have 2 disks and are copying between them. It will also help if you are dealing with large disk arrays that can take multiple r/w simultanously.

It will help if you are copying between 2 network locations using your machine as a proxy.

It will help if you are copying from network or local to a network drive.

It will somewhat help if you are copying between local and network.

### Where will crazy-large buffers help

This was actually done to help create large files for virtual machines. Setting buffer size to 1GB during copy to a netapp filer where these were hosted, made the virtual machines run much faster as the files were not as fragmented (single 1GB flush will attempt on most filesystems to find a continuous 1GB space if possible to minimise fragmentation).

So, cases like this one. To fight off fragmentation. Most filesystems play nice and properly handle large buffer write calls by ensuring fragmentation is minimal.

### Usage

Get the binary for your OS from the build job artifacts. See https://gitlab.com/bestmethod/go-acp/-/jobs - with a download button on the right-hand side. Or enter the job and "Browse" artifacts.

```
$ ./acp --help
2018/06/12 16:30:29 Usage:
  acp [OPTIONS] source_file [source_file [...]] destination_file

Application Options:
  -e, --report-each       Report as each file has completed being copied (for multi-file copy using recursive or config file)
  -s, --buffer-size=      Select buffer size to use. For SSDs optimal speed is 131072 (128KB). Use multiples of (e.g. 256KB, 1MB). Bigger buffer = less fragmentation. RAM required = buffer*2. (default: 131072)
  -p, --progress=         Print progress of copy operation ever X seconds. Disabled=0. (default: 1)
  -w, --print-raw         If set, will not print progres in human-readable format (e.g. 1MB) but always in bytes.
  -m, --override-mode     If set, will override permissions of destination if it already exists. Otherwise, will preserve existing destination permissions.
  -d, --delete-first      If set, this will delete the destination file before writing. Useful for changing inode number and resetting ownership.
  -l, --preserve-symlink  If set, will preserve symlinks instead of resolving contents of files they point to. This will only work for 1:1 mapping of source to destination file. If copying multiple sources to one destination file, this will be silently ignored for that
                          copy. Existing and files at destination will not be overwritten by a new symlink, unless DeleteFirst is specified.

Help Options:
  -h, --help              Show this help message

Copy behaviour:
  * file(s) -> dir	copy files to directory
  * file(s) -> file	copy files to file, concat mode
  * file(s) -> new-name	copy files to file, concat mode
  * dir(s)  -> file	error, no really
  * dir(s)  -> dir	copy directories' contents to dir (so acp a b, will result in a/* being in b, not b/a/*)
  * dir(s)  -> new-name	copy directories' contents to dir (so acp a b, will result in a/* being in b, not b/a/*)`) 
```
