package main

import (
	"fmt"
	"github.com/jessevdk/go-flags"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ReportFileStartDone bool `short:"e" long:"report-each" description:"Report as each file has completed being copied (for multi-file copy using recursive or config file)"`
	BufSize             int  `short:"s" long:"buffer-size" description:"Select buffer size to use. For SSDs optimal speed is 131072 (128KB). Use multiples of (e.g. 256KB, 1MB). Bigger buffer = less fragmentation. RAM required = buffer*2." default:"131072"`
	PrintProgress       int  `short:"p" long:"progress" description:"Print progress of copy operation ever X seconds. Disabled=0." default:"1"`
	PrintRawNumbers     bool `short:"w" long:"print-raw" description:"If set, will not print progres in human-readable format (e.g. 1MB) but always in bytes."`
	OverridePermissions bool `short:"m" long:"override-mode" description:"If set, will override permissions of destination if it already exists. Otherwise, will preserve existing destination permissions."`
	DeleteFirst         bool `short:"d" long:"delete-first" description:"If set, this will delete the destination file before writing. Useful for changing inode number and resetting ownership."`
	PreserveSymlink     bool `short:"l" long:"preserve-symlink" description:"If set, will preserve symlinks instead of resolving contents of files they point to. This will only work for 1:1 mapping of source to destination file. If copying multiple sources to one destination file, this will be silently ignored for that copy. Existing and files at destination will not be overwritten by a new symlink, unless DeleteFirst is specified."`
	Files               []string
}

var TypeDir = 1
var TypeFile = 2
var TypeLink = 3

type files struct {
	Source        string
	Destination   string
	Type          int
	fStat         os.FileInfo
	lStat         os.FileInfo
	LinkFile      string // for symlinks only, will eventually point at individual file that will be used as source
	LinkFileLstat os.FileInfo
	LinkSource    string // what the original link referenced
}

type CopyStruct struct {
	TotalSize    int64
	CopiedBytes  int64
	TimePrinted  time.Time
	BytesPrinted int64
	wg           sync.WaitGroup
	buffers      [2][]byte
	readlen      [2]int
	TimeStart    time.Time
	Conf         Config
	bufferFileNo [2]int
	Files        []files
	Concat       bool
	r2w          [2]chan int
	w2r          [2]chan int
}

func main() {
	Copy := new(CopyStruct)
	a := make(chan int,1)
	Copy.r2w[0] = a
	a = make(chan int,1)
	Copy.r2w[1] = a
	a = make(chan int,1)
	Copy.w2r[0] = a
	a = make(chan int,1)
	Copy.w2r[1] = a
	Copy.w2r[0] <- 1
	Copy.w2r[1] <- 1
	p := flags.NewParser(&Copy.Conf, flags.Default^flags.PrintErrors)
	var err error
	Copy.Conf.Files, err = p.ParseArgs(os.Args)
	if err != nil {
		s := err.Error()
		if strings.Contains(err.Error(), "unknown flag") {
			s = strings.Join([]string{s, "try: --help"}, "; ")
		}
		s = strings.Replace(s, "[OPTIONS]", "[OPTIONS] source_file [source_file [...]] destination_file", -1)
		s = fmt.Sprintf("%s\n%s",s,"Copy behaviour:")
		s = fmt.Sprintf("%s\n%s",s,"  * file(s) -> dir\tcopy files to directory")
		s = fmt.Sprintf("%s\n%s",s,"  * file(s) -> file\tcopy files to file, concat mode")
		s = fmt.Sprintf("%s\n%s",s,"  * file(s) -> new-name\tcopy files to file, concat mode")
		s = fmt.Sprintf("%s\n%s",s,"  * dir(s)  -> file\terror, no really")
		s = fmt.Sprintf("%s\n%s",s,"  * dir(s)  -> dir\tcopy directories' contents to dir (so acp a b, will result in a/* being in b, not b/a/*)")
		s = fmt.Sprintf("%s\n%s",s,"  * dir(s)  -> new-name\tcopy directories' contents to dir (so acp a b, will result in a/* being in b, not b/a/*)`)")
		log.Fatalln(s,"\n")
	}
	if Copy.Conf.BufSize <= 0 {
		log.Fatalln("ERROR: Buffer size must be a positive value.")
	}
	if len(Copy.Conf.Files) < 3 {
		log.Fatalln("ERROR: at least one file copy operation is required. Use --help")
	}
	Copy.Conf.Files = Copy.Conf.Files[1:]
	fmt.Print("Enumerating source... ")
	Copy.Concat = true
	for i := 0; i < len(Copy.Conf.Files)-1; i++ {
		Copy.Walk(Copy.Conf.Files[i], Copy.Conf.Files[len(Copy.Conf.Files)-1])
	}
	if Copy.Conf.PreserveSymlink == false {
		Copy.WalkSymlinks()
	}
	fStat, err := os.Stat(Copy.Conf.Files[len(Copy.Conf.Files)-1])
	if err == nil {
		if fStat.Mode().IsRegular() != true {
			Copy.Concat = false
		}
	}
	fmt.Println("Done!")
	if Copy.Concat == true {
		fmt.Println("Concat mode")
	}
	fmt.Println("Starting copy... ")
	Copy.buffers[0] = make([]byte, Copy.Conf.BufSize)
	Copy.buffers[1] = make([]byte, Copy.Conf.BufSize)
	Copy.wg.Add(2)
	Copy.TimeStart = time.Now()
	go Copy.readFiles()
	go Copy.writeFiles()
	Copy.wg.Wait()
	Copy.reportProgress(true)
	fmt.Println("Finished")
}

func (c *CopyStruct) reportProgress(now bool) {
	if c.Conf.PrintProgress != 0 {
		if now == true || time.Now().After(c.TimePrinted.Add(time.Duration(c.Conf.PrintProgress)*time.Second)) {
			BytesPrinted := c.BytesPrinted
			PrintElapsed := float64(time.Now().Sub(c.TimePrinted).Seconds())
			c.TimePrinted = time.Now()
			c.BytesPrinted = 0
			timeElapsed := int64(time.Now().Sub(c.TimeStart).Seconds())
			if timeElapsed == 0 {
				timeElapsed = 1
			}
			log.Printf("Copy: %s out of %s\tAverage Speed: %s/s\tCurrent Speed: %s/s\n", c.convSize(c.CopiedBytes), c.convSize(c.TotalSize), c.convSize(c.CopiedBytes/timeElapsed), c.convSize(int64(float64(BytesPrinted)/PrintElapsed)))
		}
	}
}

func (c *CopyStruct) convSize(size int64) string {
	var sizeString string
	if c.Conf.PrintRawNumbers == false {
		if size > 1023 && size < 1024*1024 {
			sizeString = fmt.Sprintf("%.2f KB", float64(size)/1024)
		} else if size < 1024 {
			sizeString = fmt.Sprintf("%v B", size)
		} else if size >= 1024*1024 && size < 1024*1024*1024 {
			sizeString = fmt.Sprintf("%.2f MB", float64(size)/1024/1024)
		} else if size >= 1024*1024*1024 {
			sizeString = fmt.Sprintf("%.2f GB", float64(size)/1024/1024/1024)
		}
	} else {
		sizeString = fmt.Sprintf("%v", size)
	}
	return sizeString
}

func (c *CopyStruct) Walk(source string, dest string) {
	var file files
	var err error
	file.Source = source
	file.Destination = dest
	file.fStat, err = os.Stat(source)
	if err != nil {
		log.Fatalln("Error accessing source file/dir: ", err)
	}
	file.lStat, err = os.Lstat(source)
	if err != nil {
		file.lStat = file.fStat
	}
	if file.lStat.Mode().IsRegular() == true {
		file.Type = TypeFile
		c.TotalSize = c.TotalSize + file.fStat.Size()
	} else if file.lStat.IsDir() == true {
		file.Type = TypeDir
		c.Concat = false
	} else if file.lStat.Mode()&os.ModeSymlink != 0 {
		file.Type = TypeLink
		if c.Conf.PreserveSymlink == true {
			c.Concat = false
		}
		file.LinkSource, err = os.Readlink(file.Source)
	} else {
		log.Fatalln("File type not supported: ", file.Source)
	}
	if file.Type == TypeDir {
		err = filepath.Walk(source, func(npath string, info os.FileInfo, err error) error {
			if err != nil {
				log.Fatalln("Error walking `", npath, "` err: ", err)
			}
			nFile := files{}
			nFile.Source = npath
			nFile.lStat = info
			nFile.fStat = nFile.lStat
			if nFile.lStat.Mode().IsRegular() == true {
				nFile.Type = TypeFile
				c.TotalSize = c.TotalSize + nFile.fStat.Size()
			} else if nFile.lStat.IsDir() == true {
				nFile.Type = TypeDir
			} else if nFile.lStat.Mode()&os.ModeSymlink != 0 {
				nFile.Type = TypeLink
				nFile.LinkSource, err = os.Readlink(nFile.Source)
				if err != nil {
					log.Fatalln("Could not resolve link destination, link:", file.Source, " err:", err)
				}
			} else {
				log.Fatalln("File type not supported: ", nFile.Source)
			}
			nFile.Destination = path.Join(dest, strings.Replace(npath, source, "", 1))
			c.Files = append(c.Files, nFile)
			return nil
		})

		if err != nil {
			log.Fatalln("Error walking `", source, "` err: ", err)
		}
	} else {
		lStat, err := os.Lstat(dest)
		if err == nil && lStat.IsDir() == true {
			// destination is dir, source is file or link
			file.Destination = path.Join(file.Destination, path.Base(file.Source))
		}
		c.Files = append(c.Files, file)
	}
}

func (c *CopyStruct) WalkSymlinks() {
	var err error
	for i, file := range c.Files {
		if file.Type == TypeLink {
			file.LinkFile = file.Source
			file.LinkFileLstat = file.lStat
			curr, _ := os.Getwd()
			for file.LinkFileLstat.Mode()&os.ModeSymlink != 0 {
				os.Chdir(path.Dir(file.LinkFile))
				file.LinkFile, err = os.Readlink(path.Base(file.LinkFile))
				if err != nil {
					log.Fatalln("Could not resolve link destination, link:", file.Source, " err:", err)
				}
				file.LinkFileLstat, err = os.Lstat(file.LinkFile)
				if err != nil {
					log.Fatalln("Error accessing source file/dir: ", err)
				}
			}
			file.LinkFile, _ = filepath.Abs(file.LinkFile)
			file.Source = file.LinkFile
			os.Chdir(curr)
			if file.LinkFileLstat.IsDir() == true {
				file.Type = TypeDir
				c.Concat = false
				c.Walk(file.LinkFile, file.Destination)
			} else if file.LinkFileLstat.Mode().IsRegular() == true {
				file.Type = TypeFile
				c.TotalSize = c.TotalSize + file.LinkFileLstat.Size()
			} else if file.LinkFileLstat.Mode()&os.ModeSymlink == 0 {
				//not a file, dir nor symlink
				log.Fatalln("File type not supported: ", file.LinkFile)
			}
			c.Files[i] = file
		}
	}
}

func (c *CopyStruct) readFiles() {
	defer c.wg.Done()
	offset := 0
	var fd *os.File
	var err error
	var tmplen int
	for ll, file := range c.Files {
		if file.Type == TypeFile {
			if c.Conf.DeleteFirst == true {
				if _, err = os.Stat(c.Files[ll].Destination); err == nil {
					err = os.Remove(c.Files[ll].Destination)
					if err != nil {
						log.Fatalln("ERROR: Could not remove file before copying")
					}
				}
			}
			lst := file.lStat
			if lst.Size() == 0 {
				if c.readlen[offset] == 0 {
					c.readlen[offset] = -2
					c.bufferFileNo[offset] = ll
					if offset == 0 {
						offset = 1
					} else {
						offset = 0
					}
				}
			} else {
				var f string
				f = file.Source
				if fd, err = os.Open(f); err != nil {
					log.Fatalf("ERROR: cannot open source file %s\n", f)
				}
				for err != io.EOF {
					<-c.w2r[offset]
					if c.readlen[offset] == 0 {
						tmplen, err = fd.Read(c.buffers[offset])
						if err != nil && err != io.EOF {
							fmt.Printf("ERROR reading from file: %s\n", err)
							os.Exit(2)
						}
						if tmplen != 0 {
							c.bufferFileNo[offset] = ll
							c.readlen[offset] = tmplen
							c.r2w[offset] <- 1
							if offset == 0 {
								offset = 1
							} else {
								offset = 0
							}
						}
					}
				}
				fd.Close()
			}
		}
	}
}

func (c *CopyStruct) writeFiles() {
	defer c.wg.Done()
	var fd *os.File
	var err error
	offset := 0
	ll := -1
	llst := -1
	for c.TotalSize > c.CopiedBytes {
		go c.reportProgress(false)
		<-c.r2w[offset]
		if c.readlen[offset] != 0 {
			if ll != c.bufferFileNo[offset] {
				if c.Conf.ReportFileStartDone == true {
					if ll != -1 {
						fmt.Printf("File Complete: %s > %s\n", c.Files[ll].Source, c.Files[ll].Destination)
					}
					fmt.Printf("File Start: %s > %s\n", c.Files[c.bufferFileNo[offset]].Source, c.Files[c.bufferFileNo[offset]].Destination)
				}
				if c.Concat == false {
					fd.Close()
				}
				llst = ll
				ll = c.bufferFileNo[offset]
				for bb := llst + 1; bb < ll; bb++ {
					//something was skipped, create dir/symlink
					if c.Files[bb].Type == TypeDir {
						if c.Conf.ReportFileStartDone == true {
							fmt.Printf("Create dir: %s\n", c.Files[bb].Destination)
						}
						os.MkdirAll(c.Files[bb].Destination, c.Files[bb].lStat.Mode()|os.ModeSymlink)
					} else if c.Files[bb].Type == TypeLink {
						if c.Conf.ReportFileStartDone == true {
							fmt.Printf("Link: %s -> %s\n", c.Files[bb].LinkSource, c.Files[bb].Destination)
						}
						if _, err = os.Stat(c.Files[bb].Destination); err == nil {
							err = os.Remove(c.Files[bb].Destination)
							if err != nil {
								fmt.Println("ERROR: Could not remove file before copying")
								os.Exit(3)
							}
						}
						err = os.Symlink(c.Files[bb].LinkSource, c.Files[bb].Destination)
						if err != nil {
							log.Fatalln("Could not create symlink: ", c.Files[bb].Destination, " err: ", err)
						}
					}
				}
				fStat := c.Files[ll].lStat
				if c.Conf.DeleteFirst == true {
					if _, err = os.Stat(c.Files[ll].Destination); err == nil {
						err = os.Remove(c.Files[ll].Destination)
						if err != nil {
							fmt.Println("ERROR: Could not remove file before copying")
							os.Exit(3)
						}
					}
				}
				fStatMode := fStat.Mode() | os.ModeSymlink
				if c.Concat == false || fd == nil {
					fd, err = os.OpenFile(c.Files[ll].Destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fStatMode)
				}
				if c.Conf.OverridePermissions == true {
					os.Chmod(c.Files[ll].Destination, fStatMode)
				}
				if err != nil {
					fmt.Printf("ERROR opening file for writing: %s; %s\n", c.Files[ll].Destination, err)
					os.Exit(3)
				}
			}
			if c.readlen[offset] == -2 {
				fd.Write([]byte{})
			} else {
				fd.Write(c.buffers[offset][:c.readlen[offset]])
				c.CopiedBytes = c.CopiedBytes + int64(c.readlen[offset])
				c.BytesPrinted = c.BytesPrinted + int64(c.readlen[offset])
			}
			c.readlen[offset] = 0
			c.w2r[offset] <- 1
			if offset == 0 {
				offset = 1
			} else {
				offset = 0
			}
		}
	}
	if ll != -1 && c.Conf.ReportFileStartDone == true {
		fmt.Printf("File Complete: %s > %s\n", c.Files[ll].Source, c.Files[ll].Destination)
	}
	if ll == -1 {
		ll = 0
	}
	for j := ll; j < len(c.Files); j++ {
		//something was skipped, create dir/symlink
		if c.Files[j].Type == TypeDir {
			if c.Conf.ReportFileStartDone == true {
				fmt.Printf("Create dir: %s\n", c.Files[j].Destination)
			}
			os.MkdirAll(c.Files[j].Destination, c.Files[j].lStat.Mode()|os.ModeSymlink)
		} else if c.Files[j].Type == TypeLink {
			if c.Conf.ReportFileStartDone == true {
				fmt.Printf("Link: %s -> %s\n", c.Files[j].LinkSource, c.Files[j].Destination)
			}
			if _, err = os.Stat(c.Files[j].Destination); err == nil {
				err = os.Remove(c.Files[j].Destination)
				if err != nil {
					fmt.Println("ERROR: Could not remove file before copying")
					os.Exit(3)
				}
			}
			err = os.Symlink(c.Files[j].LinkSource, c.Files[j].Destination)
			if err != nil {
				log.Fatalln("Could not create symlink: ", c.Files[j].Destination, " err: ", err)
			}
		}
	}
	if c.Concat == true {
		fd.Close()
	}
}
