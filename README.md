# Auto Provisioning Agent For Choria Server

Choria Server supports a Provisioning mode that assists bootstrapping the system in large environments or dynamic cloud based environments that might not be under strict CM control.

When an unconfigured Choria Server is in provisioning mode it will connect to a compiled-in Middleware network and join the `provisioning` sub collective.  It will optionally publish it's metadata and expose it's facts.

You can think of this as a similar setup as the old Provisioning VLANs where new servers would join to PXE boot etc. but here we expose an API that let the provisioning environment control the node.

The idea is that an automated system will discover nodes in the `provisioning` sub collective and guide them through the on-boarding process.  The on-boarding process can be entirely custom, one possible flow might be:

  * Discover all nodes in the `provisioning` subcollective with the `choria_provision` agent
    * For every discovered nodes
        * Retrieve facts and metadata
        * Based on it's facts programmatically determine which Member Collective in a [Federation](https://choria.io/docs/federation/) this node should belong to.
        * Ask the node for a CSR, potentially supplying a custom CN, OU, O, C and L
        * Sign the CSR the node provided against your own CA
        * Construct a configuration tailored to this node, setting things like SRV domain or hard coded brokers
        * Send the configuration, certificate and CA chain to the node where it will configure itself
        * Request the node restarts itself within a provided splay time

After this flow the node will join it's configured Member Collective with it's signed Certificate and CA known it becomes a normal node like any other.

You can invoke the `choria_provision#reprovision` action to ask it to leave its Member Collective and re-enter the provisioning flow.

**WARNING** At present Choria Server does not yet support something like the Action Policy ACL system and inherently during provision there is no CA to provide Authentication against. While a token is supported the token is easily found in the `choria` binary.  Keep this in mind before adopting this approach.

## Configuring Choria Server

Provisioning is off and cannot be enabled in the version of Choria shipped to the Open Source community, to use it you need to perform a custom build and make your own packages.  Choria provides the tools to do this.

The following section guides you through setting up a custom build that will produce a `acme-choria` RPM with completely custom paths etc.  It will have provisioning enabled and whenever it detects `plugin.choria.server.provision` is not set to `false` will enter provisioning mode by connecting to `choria-provision.example.net:4222`.

### Creating a custom build specification

The build specification is in the `go-choria` repository in `packager/buildspec.yaml`, lets see a custom one:

```yaml
flags_map:
  TLS: github.com/choria-io/go-choria/build.TLS
  maxBrokerClients: github.com/choria-io/go-choria/build.maxBrokerClients
  Secure: github.com/choria-io/go-choria/vendor/github.com/choria-io/go-protocol/protocol.Secure
  Version: github.com/choria-io/go-choria/build.Version
  SHA: github.com/choria-io/go-choria/build.SHA
  BuildTime: github.com/choria-io/go-choria/build.BuildDate
  ProvisionBrokerURLs: github.com/choria-io/go-choria/build.ProvisionBrokerURLs
  ProvisionModeDefault: github.com/choria-io/go-choria/build.ProvisionModeDefault
  ProvisionAgent: github.com/choria-io/go-choria/build.ProvisionAgent
  ProvisionSecure: github.com/choria-io/go-choria/build.ProvisionSecure
  ProvisionRegistrationData: github.com/choria-io/go-choria/build.ProvisionRegistrationData
  ProvisionFacts: github.com/choria-io/go-choria/build.ProvisionFacts
  ProvisionToken: github.com/choria-io/go-choria/build.ProvisionToken

foss:
  compile_targets:
    defaults:
      output: choria-{{version}}-{{os}}-{{arch}}
      pre:
        - rm additional_agent_*.go || true
        - go generate
      flags:
        ProvisionModeDefault: "true"
        ProvisionBrokerURLs: "choria-provision.example.net:4222"
        ProvisionSecure: "false"
        ProvisionRegistrationData: "/opt/acme/etc/node-metadata.json"
        ProvisionFacts: "/opt/acme/etc/node-metadata.json"
        ProvisionToken: "toomanysecrets"

    64bit_linux:
      os: linux
      arch: amd64

  packages:
    defaults:
      name: acme-choria
      bindir: /opt/acme-choria/sbin
      etcdir: /opt/acme-choria/etc
      release: 1
      manage_conf: 1
      contact: admins@example.net
      rpm_group: Acme/Tools

    el7_64:
      template: el/el7
      dist: el7
      target_arch: x86_64
      binary: 64bit_linux
```

This is a stripped down packaging config based on the stock one, it will:

  * Build only a 64bit Linux binary
  * Package a el7 64bit RPM with the name `acme-choria` and custom paths
  * Provisioning is on by default unless specifically disabled in the configuration
  * It will use this agent by default to enable provisioning, you can supply your own see below
  * It will connect to `choria-provision.example.net:4222` with TLS disabled
  * It will publish regularly the file `/opt/acme/etc/node-metadata.json` to `choria.provisioning_data` on the middleware
  * It will use `/opt/acme/etc/node-metadata.json` as a fact source so you can discover it or retrieve its facts using `rpcutil#inventory` action

### Using your own agent

You might not like the provisioning flow exposed by this agent, no problem you can supply your own.

Create `packaging/agents.yaml`

```yaml
---
agents:
- name: choria_provision
  repo: github.com/acme/prov_agent
```

Arrange for this to be in the project using `glide get` and in the `buildspec.yaml` set `ProvisionAgent: "false"` in the flag section, it will now not activate this agent and instead use yours.

### Building

Do a `rake build` (needs docker) and after some work you'll have a rpm tailored to your own paths, name and with Provisioning enabled.

```
$ choria buildinfo
# ...
Server Settings:
            Provisioning Brokers: choria-provision.example.net:4222
            Provisioning Default: true
      Default Provisioning Agent: true
                Provisioning TLS: false
  Provisioning Registration Data: /opt/acme/etc/node-metadata.json
              Provisioning Facts: /opt/acme/etc/node-metadata.json
# ...
```

If you just want the binary and no packages use `rake build_binaries`.

## Provisioning nodes

The agent has the following actions:

  * **gencsr** - generates a private key and CSR on the node, returns the CSR and directory they were stored in
  * **configure** - configures a node with the given configuration, signed certificate and ca and path to the ssl store
  * **restart** - restarts the server after a random splay
  * **reprovision** - re-enter provisioning mode

Each action takes an optional token which should match that compiled into the Choria binary via the `ProvisionToken` flag.

The intention is that you'll write a bit of code - a daemon in effect - that repeatedly scans the provisioning network for new nodes and provision them.  For a evented approach you can listen on the `choria.provisioning_data` topic for node registration data and discover them this way.

We'll show a few snippets for how you might achieve a flow here.  This code is in Ruby and uses the [Choria RPC Client](https://choria.io/docs/development/mcorpc/clients/)

### Discovery Provisionable Nodes

All agents will join your target broker in the `provisioning` collective, all nodes there should be configurable really:

```ruby
c = rpcclient("choria_provision")

# force the client into the right collective, your client
# config should have this in the list of known collectives
c.collective = "provisioning"

nodes = c.discover
```

### Get the CSR from the nodes and sign it using [cfssl](https://github.com/cloudflare/cfssl)

Above we got the list of configurable nodes, lets look how the per node iteration looks starting with CSR

```ruby
def request_success?(stats)
  return false if stats.failcount > 0
  return false if stats.okcount == 0
  return false unless stats.noresponsefrom.empty?

  true
end

def signcsr(node, client)
    # get the client to talk to just the one node
    client.discover(:nodes => [node])

    # ask the node for a CSR
    result = client.gencsr(:cn => node, :O => "Devco", :OU => "Orchestration", :token => @token).first
    unless request_success?(client.stats)
      raise("CSR Request failed: %s" % result[:statusmsg])
    end

    csr = result[:data][:csr]

    # the node will construct a ssl dir relative to its config file if not configured
    # and tell us where that is so we can use this later to make the configuration
    ssldir = result[:data][:ssldir]

    # here we use cfssl to sign it, we should use the HTTP API and not save it to the
    # disk or shell out, but lets keep it easy
    File.open("%s.csr" % node, "w") do |f|
      f.print csr
    end

    signed = JSON.parse(%x[cfssl sign -ca ca.pem -ca-key ca-key.pem #{node}.csr])
    unless signed["cert"]
      raise("No signed certificate received from cfssl")
    end

    return [ssldir, signed]
end
```

### Get facts and generate a configuration

We'll fetch the facts from the node and use it to construct a configuration for it, facts are exposed via the `rpcutil#inventory` action.

```ruby
def genconfig(node, ssldir)
    client = rpcclient("rpcutil")
    client.discover(:nodes => [node])

    # ask the node for its inventory
    result = client.inventory().first
    unless request_success?(client.stats)
      raise("Inventory request failed: %s" % result[:statusmsg])
    end

    facts = result[:data][:facts]

    # do some permutation based on facts, keeping it simple here
    srvdomain = "example.net"
    srvdomain = "dev.example.net" if facts["environment"] == "development"

    {
      # you must disable provisioning in the config as its on by default at compile time
      "plugin.choria.server.provision" => "false",

      # custom SSL config, not using Puppet CA here
      "plugin.security.provider" => "file",
      "plugin.security.file.certificate" => File.join(ssldir, "certificate.pem"),
      "plugin.security.file.key" => File.join(ssldir, "private.pem"),
      "plugin.security.file.ca" => File.join(ssldir, "ca.pem"),

      # srv domain based on facts
      "plugin.choria.srv_domain" => srvdomain,

      # rest standard stuff
      "classesfile" => "/opt/puppetlabs/puppet/cache/state/classes.txt",
      "collectives" => "mcollective",
      "identity" => facts["fqdn"],
      "logfile" => "/var/log/choria.log",
      "loglevel" => "info",
      "plugin.choria.agent_provider.mcorpc.agent_shim" => "/usr/bin/choria_mcollective_agent_compat.rb",
      "plugin.choria.agent_provider.mcorpc.config" => "/etc/puppetlabs/mcollective/choria-shim.cfg",
      "plugin.choria.agent_provider.mcorpc.libdir" => "/opt/puppetlabs/mcollective/plugins",
      "plugin.rpcaudit.logfile" => "/var/log/choria-audit.log",
      "plugin.yaml" => "/etc/puppetlabs/mcollective/generated-facts.yaml",
      "rpcaudit" => "1"
    }
end
```

### Configure the node

With the above SSL and configuration we can look at configuring the node:

```ruby
def configure(node, config, cert, ca, ssldir, client)
    client.discover(:nodes => [node])

    # we pass the config, CA, Signed Certificate and SSL directory onto the node
    result = client.configure(:ca => ca.chomp, :certificate => cert.chomp, :config => config.to_json, :ssldir => ssldir, :token => @token).first
    unless request_success?(client.stats)
      raise("Configuration Request failed: %s" % result[:statusmsg])
    end
end
```

### Restart the node with a short splay

Once configured we can restart the Choria Server on the node

```ruby
def restart(node, client)
  client.discover(:nodes => [node])

  result = client.restart(:splay => 2, :token => @token)
  unless request_success?(client.stats)
    raise("Restart Request failed: %s" % result[:statusmsg])
  end
end
```

### Pulling it all together

```ruby
#!/opt/puppetlabs/puppet/bin/ruby

# our provisioning network has no TLS, tell the choria libraries to disable it
$choria_unsafe_disable_nats_tls = true

# the token as compiled into the choria binaries
@token = "toomanysecrets"

require "mcollective"

include MCollective::RPC

c = rpcclient("choria_provision")
c.collective = "provisioning"
ca = File.read("ca.pem")

# above methods not shown

while true
  nodes = c.discover

  nodes.each do |node|
    begin
      ssldir, signed = signcsr(node, c)
      config = genconfig(node, ssldir)
      configure(node, config, signed, ca, ssldir, c)
      restart(node, c)
    rescue
      STDOUT.puts("Failed to provision %s: %s", [node, $!.to_s])
    end
  end

  c.reset
end
```

When you run this it will forever find all nodes and provision them.  In the real world you'll have something more complex but I wanted to show how this flow can work to take nodes from unconfigured to fully functional.