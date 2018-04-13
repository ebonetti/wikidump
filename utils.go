package wikidump

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/kjk/lzmadec"
	"github.com/pkg/errors"
)

func unGZip(ri io.ReadCloser) (io.ReadCloser, error) {
	ro, err := gzip.NewReader(ri)
	if err != nil {
		ri.Close()
		return nil, err
	}
	return readClose{ro, func() error {
		err1 := ro.Close()
		err0 := ri.Close()
		if err1 != nil {
			return err1
		}
		return err0
	}}, nil
}

func unBZip2(r io.ReadCloser) (io.ReadCloser, error) {
	return readClose{bzip2.NewReader(bufio.NewReader(r)), r.Close}, nil
}

func un7Zip(ri io.ReadCloser) (ro io.ReadCloser, err error) {
	fail := func(e error) (io.ReadCloser, error) {
		ri.Close()
		ro, err = nil, e
		return ro, err
	}

	//Kludgy: it doesn't exist a fully proofed golang version, so we need to reach the file itself
	fname, err := filename(ri)
	if err != nil {
		return fail(err)
	}

	archive, err := lzmadec.NewArchive(fname)
	if err != nil {
		return fail(errors.Wrapf(err, "%v while listing content of file %v", lzmadecErr2Meaning(err), fname))
	}

	if len(archive.Entries) != 1 {
		return fail(errors.Errorf("Error entries count differs from one - %v - for file %v", len(archive.Entries), fname))
	}

	r, err := archive.GetFileReader(archive.Entries[0].Path)
	if err != nil {
		return fail(errors.Wrapf(err, "%v while opening file %v", lzmadecErr2Meaning(err), fname))
	}

	return readClose{r, func() error {
		err1 := ri.Close()
		err0 := os.Remove(fname)
		if err1 != nil {
			return err1
		}
		return err0
	}}, nil
}

func filename(r io.ReadCloser) (string, error) {
	rc, ok := r.(readClose)
	if !ok {
		return "", errors.New("Unable to cast to readClose")
	}
	f, ok := rc.Reader.(*os.File)
	if !ok {
		return "", errors.New("Unable to cast to *os.File")
	}
	f.Close()
	return f.Name(), nil
}

func lzmadecErr2Meaning(err error) (defaultM string) {
	if err == nil {
		return
	}

	defaultM = "Error"

	exiterr, ok := err.(*exec.ExitError)
	if !ok {
		return
	}

	// This works on both Unix and Windows. Although package
	// syscall is generally platform dependent, WaitStatus is
	// defined for both Unix and Windows and in both cases has
	// an ExitStatus() method with the same signature.
	status, ok := exiterr.Sys().(syscall.WaitStatus)
	if !ok {
		return
	}

	m, ok := code2Meaning[status.ExitStatus()]
	if !ok {
		return
	}

	return m
}

var code2Meaning = map[int]string{
	1:   "Warning",
	2:   "Fatal error",
	3:   "Change identified",
	7:   "Command line error",
	8:   "Not enough memory for operation",
	255: "User stopped the process",
}

type readClose struct {
	io.Reader
	Closer func() error
}

func (r readClose) Close() error {
	return r.Closer()
}