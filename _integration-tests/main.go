// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2015 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	image "./image"
	utils "./utils"
)

const (
	baseDir        = "/tmp/snappy-test"
	testsBinDir    = "_integration-tests/bin/"
	defaultRelease = "rolling"
	defaultChannel = "edge"
	defaultSSHPort = 22
	defaultGoArm   = "7"
	dataOutputDir  = "_integration-tests/data/output/"
	controlTpl     = "_integration-tests/data/tpl/control"
)

var (
	commonSSHOptions = []string{"---", "ssh"}
	configFileName   = filepath.Join(dataOutputDir, "testconfig.json")
	controlFile      = filepath.Join(dataOutputDir, "control")
	testPackages     = []string{"latest", "failover"}
)

func buildAssets(useSnappyFromBranch bool, arch string) {
	utils.PrepareTargetDir(testsBinDir)

	if useSnappyFromBranch {
		// FIXME We need to build an image that has the snappy from the branch
		// installed. --elopio - 2015-06-25.
		buildSnappyCLI(arch)
	}
	buildTests(arch)
}

func writeTestConfig(release, channel, targetRelease, targetChannel string) {
	fmt.Println("Writing test config...")
	testConfig := map[string]string{
		"release": release,
		"channel": channel,
	}
	if targetRelease != "" {
		testConfig["targetRelease"] = targetRelease
	}
	if targetChannel != "" {
		testConfig["targetChannel"] = targetChannel
	}
	fmt.Println(testConfig)
	encoded, err := json.Marshal(testConfig)
	if err != nil {
		log.Fatalf("Error encoding the test config: %v", testConfig)
	}
	ioutil.WriteFile(configFileName, encoded, 0644)
}

func setupAndRunLocalTests(rootPath, testFilter string, img image.Image) {
	var includeShell bool
	if testFilter == "" {
		includeShell = true
	}

	// Run the tests on the latest rolling edge image.
	if imageName, err := img.UdfCreate(); err == nil {
		adtRun(rootPath, testFilter, testPackages,
			kvmSSHOptions(imageName), includeShell)
	}
}

func setupAndRunRemoteTests(rootPath, testFilter, testbedIP string, testbedPort int) {
	utils.ExecCommand("ssh-copy-id", "-p", strconv.Itoa(testbedPort),
		"ubuntu@"+testbedIP)
	adtRun(rootPath, testFilter, testPackages,
		remoteTestbedSSHOptions(testbedIP, testbedPort), true)
}

func buildSnappyCLI(arch string) {
	fmt.Println("Building snappy CLI...")
	// On the root of the project we have a directory called snappy, so we
	// output the binary for the tests in the tests directory.
	goCall(arch, "build", "-o", testsBinDir+"snappy", "./cmd/snappy")
}

func buildTests(arch string) {
	fmt.Println("Building tests...")

	for _, testName := range testPackages {
		goCall(arch, "test", "-c",
			"./_integration-tests/tests/"+testName)
		// XXX Go test 1.3 does not have the output flag, so we move the
		// binaries after they are generated.
		os.Rename(testName+".test", testsBinDir+testName+".test")
	}
}

func goCall(arch string, cmds ...string) {
	if arch != "" {
		defer os.Setenv("GOARCH", os.Getenv("GOARCH"))
		os.Setenv("GOARCH", arch)
		if arch == "arm" {
			defer os.Setenv("GOARM", os.Getenv("GOARM"))
			os.Setenv("GOARM", defaultGoArm)
		}
	}
	goCmd := append([]string{"go"}, cmds...)
	utils.ExecCommand(goCmd...)
}

func adtRun(rootPath, testFilter string, testList, testbedOptions []string, includeShell bool) {
	createControlFile(testFilter, testList, includeShell)

	fmt.Println("Calling adt-run...")
	outputSubdir := getOutputSubdir(testList, includeShell)
	outputDir := filepath.Join(baseDir, "output", outputSubdir)
	utils.PrepareTargetDir(outputDir)

	cmd := []string{
		"adt-run", "-B",
		"--setup-commands", "touch /run/autopkgtest_no_reboot.stamp",
		"--override-control", controlFile,
		"--built-tree", rootPath,
		"--output-dir", outputDir}

	utils.ExecCommand(append(cmd, testbedOptions...)...)
}

func kvmSSHOptions(imagePath string) []string {
	return append(
		commonSSHOptions,
		[]string{
			"-s", "/usr/share/autopkgtest/ssh-setup/snappy",
			"--", "-i", imagePath}...)
}

func createControlFile(testFilter string, testList []string, includeShellTest bool) {
	type controlData struct {
		Filter       string
		Tests        []string
		IncludeShell bool
	}

	tpl, err := template.ParseFiles(controlTpl)
	if err != nil {
		log.Fatalf("Error reading adt-run control template %s", controlTpl)
	}

	outputFile, err := os.Create(controlFile)
	if err != nil {
		log.Fatalf("Error creating control file %s", controlFile)
	}
	defer outputFile.Close()

	err = tpl.Execute(outputFile, controlData{Filter: testFilter, Tests: testList, IncludeShell: includeShellTest})
	if err != nil {
		log.Fatalf("execution: %s", err)
	}
}

func getOutputSubdir(testList []string, includeShell bool) string {
	output := strings.Join(testList, "-")
	if includeShell {
		output = output + "-shell"
	}
	return output
}

func remoteTestbedSSHOptions(testbedIP string, testbedPort int) []string {
	options := []string{
		"-H", testbedIP,
		"-p", strconv.Itoa(testbedPort),
		"-l", "ubuntu",
		"-i", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
		"--reboot"}
	return append(commonSSHOptions, options...)
}

func getRootPath() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	return dir
}

func main() {
	var (
		useSnappyFromBranch = flag.Bool("snappy-from-branch", false,
			"If this flag is used, snappy will be compiled from this branch, copied to the testbed and used for the tests. Otherwise, the snappy installed with the image will be used.")
		arch = flag.String("arch", "",
			"Architecture of the test bed. Defaults to use the same architecture as the host.")
		testbedIP = flag.String("ip", "",
			"IP of the testbed. If no IP is passed, a virtual machine will be created for the test.")
		testbedPort = flag.Int("port", defaultSSHPort,
			"SSH port of the testbed. Defaults to use port "+strconv.Itoa(defaultSSHPort))
		testFilter = flag.String("filter", "",
			"Suites or tests to run, for instance MyTestSuite, MyTestSuite.FirstCustomTest or MyTestSuite.*CustomTest")
		imgRelease = flag.String("release", defaultRelease,
			"Release of the image to be built, defaults to "+defaultRelease)
		imgChannel = flag.String("channel", defaultChannel,
			"Channel of the image to be built, defaults to "+defaultChannel)
		imgRevision = flag.String("revision", "",
			"Revision of the image to be built (can be relative to the latest available revision in the given release and channel as in -1), defaults to the empty string")
		targetRelease = flag.String("target-release", "",
			"If specified, the image will be updated to this release before running the tests.")
		targetChannel = flag.String("target-channel", "",
			"If specified, the image will be updated to this channel before running the tests.")
	)

	flag.Parse()

	buildAssets(*useSnappyFromBranch, *arch)

	// TODO: generate the files out of the source tree. --elopio - 2015-07-15
	utils.PrepareTargetDir(dataOutputDir)
	defer os.RemoveAll(dataOutputDir)

	// TODO: pass the config as arguments to the test binaries.
	// --elopio - 2015-07-15
	writeTestConfig(*imgRelease, *imgChannel, *targetRelease, *targetChannel)

	rootPath := getRootPath()

	if *testbedIP == "" {
		img := image.NewImage(*imgRelease, *imgChannel, *imgRevision, baseDir)
		setupAndRunLocalTests(rootPath, *testFilter, *img)

	} else {
		setupAndRunRemoteTests(rootPath, *testFilter, *testbedIP, *testbedPort)
	}
}
