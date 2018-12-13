// Package wikidump provides utility functions for downloading and extracting wikipedia dumps.
package wikidump

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// Wikidump represent a hub from which request particular dump files of wikipedia.
type Wikidump struct {
	file2Info map[string][]fileInfo
	tmpDir    string
	date      time.Time
}

type fileInfo struct {
	URL, SHA1 string
}

//CheckFor checks for file existence in the wikidump
func (w Wikidump) CheckFor(filenames ...string) error {
	for _, filename := range filenames {
		if _, ok := w.file2Info[filename]; !ok {
			return errors.New(filename + " not found")
		}
	}
	return nil
}

//Date returns the date of the current Dump
func (w Wikidump) Date() time.Time {
	return w.date
}

//Open returns an iterator over the resources associated with the current filename,
//the download can be stopped by the context. Once the iterator is depleted, it returns an io.EOF error.
//Once an error is returned by the iterator, any subsequent call will return the same error.
//It is the caller's responsibility to call Close on the Reader when done.
//Open takes care of checking SHA1 sum, retry download and decompressing files.
func (w Wikidump) Open(filename string) func(context.Context) (io.ReadCloser, error) {
	ffi, err := w.file2Info[filename], w.CheckFor(filename)
	return func(ctx context.Context) (io.ReadCloser, error) {
		if err != nil {
			return nil, err
		}
		if len(ffi) == 0 {
			err = io.EOF
			return nil, err
		}
		var r io.ReadCloser
		r, err = w.open(ctx, ffi[0])
		ffi = ffi[1:]
		return r, err
	}
}

func (w Wikidump) open(ctx context.Context, fi fileInfo) (r virtualFile, err error) {
	r, err = w.stubbornStore(ctx, fi)
	switch {
	case err != nil:
		//do nothing
	case strings.HasSuffix(fi.URL, ".7z"):
		r, err = un7Zip(r)
	case strings.HasSuffix(fi.URL, ".bz2"):
		r, err = unBZip2(r)
	case strings.HasSuffix(fi.URL, ".gz"):
		r, err = unGZip(r)
	}

	return
}

func (w Wikidump) stubbornStore(ctx context.Context, fi fileInfo) (r virtualFile, err error) {
	for t := time.Second; t < time.Hour; t = t * 2 { //exponential backoff
		if r, err = w.store(ctx, fi); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return virtualFile{}, errors.Wrap(ctx.Err(), "Error: change in context state")
		case <-time.After(t):
			//do nothing
		}
	}
	return
}

func (w Wikidump) store(ctx context.Context, fi fileInfo) (r virtualFile, err error) {
	tempFile, err := ioutil.TempFile(w.tmpDir, path.Base(fi.URL))
	if err != nil {
		return virtualFile{}, errors.Wrap(err, "Error: unable to create temporary file in "+w.tmpDir)
	}
	fclose := func() error {
		err1 := errors.Wrapf(tempFile.Close(), "Error while closing reader of file %v", tempFile.Name())
		err0 := errors.Wrapf(os.Remove(tempFile.Name()), "Error while removing file %v", tempFile.Name())
		if err1 != nil {
			return err1
		}
		return err0
	}
	fail := func(e error) (virtualFile, error) {
		fclose()
		r, err = virtualFile{}, e
		return r, err
	}

	body, err := stream(ctx, fi)
	if err != nil {
		return fail(err)
	}
	defer body.Close()

	hash := sha1.New()
	_, err = io.Copy(io.MultiWriter(tempFile, hash), body)
	if err != nil {
		return fail(errors.Wrap(err, "Error: unable to copy to file the following url: "+fi.URL))
	}

	if fmt.Sprintf("%x", hash.Sum(nil)) != fi.SHA1 {
		return fail(errors.New("Error: mismatched SHA1 for the file downloaded from the following url: " + fi.URL))
	}

	if err = tempFile.Close(); err != nil {
		return fail(errors.Wrap(err, "Error: unable to close the following file: "+tempFile.Name()))
	}

	//Check file SHA1 just for testing purposes (to be removed)
	checkFileSHA1(tempFile.Name(), fi.SHA1)

	if tempFile, err = os.Open(tempFile.Name()); err != nil {
		return fail(errors.Wrap(err, "Error: unable to open the following file: "+tempFile.Name()))
	}

	return virtualFile{tempFile, fclose, tempFile.Name()}, nil
}

func stream(ctx context.Context, fi fileInfo) (r io.ReadCloser, err error) {
	req, err := http.NewRequest("GET", fi.URL, nil)
	if err != nil {
		err = errors.Wrap(err, "Error: unable create a request with the following url: "+fi.URL)
		return
	}

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		err = errors.Wrap(err, "Error: unable do a request with the following url: "+fi.URL)
		return
	}

	r = resp.Body
	return
}

func checkFileSHA1(fname, SHA1 string) {
	f, err := os.Open(fname)
	if err != nil {
		fmt.Print("Warning: unable to open the following file: "+f.Name(), err)
		return
	}
	defer f.Close()

	hash := sha1.New()
	if _, err := io.Copy(hash, f); err != nil {
		fmt.Print("Warning: unable to SHA1 the following file: "+f.Name(), err)
		return
	}

	if fmt.Sprintf("%x", hash.Sum(nil)) != SHA1 {
		fmt.Print("Warning: mismatched SHA1 for the file: "+f.Name(), err)
	}
}
