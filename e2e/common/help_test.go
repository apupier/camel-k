// +build integration

// To enable compilation of this file in Goland, go to "Settings -> Go -> Vendoring & Build Tags -> Custom Tags" and add "integration"

/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import (
	"testing"

	. "github.com/apache/camel-k/e2e/support"
	. "github.com/onsi/gomega"
)

func TestCheckHelp(t *testing.T) {
	t.Run("check help", func(t *testing.T) {
		RegisterTestingT(t)
		Expect(Kamel("help").Execute()).Should(BeNil())
	})
}

func TestCheckDashHelp(t *testing.T) {
	t.Run("check -h", func(t *testing.T) {
		RegisterTestingT(t)
		Expect(Kamel("-h").Execute()).Should(BeNil())
	})
}

func TestCheckDashDashHelp(t *testing.T) {
	t.Run("check --help", func(t *testing.T) {
		RegisterTestingT(t)
		Expect(Kamel("--help").Execute()).Should(BeNil())
	})
}
