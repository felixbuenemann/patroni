## Exhibitor-based ZooKeeper ensemble provider.
##
## This module extends ZooKeeper DCS with Exhibitor ensemble discovery.

import std/[algorithm, httpclient, json, random, sequtils, strformat, strutils, tables, times]
import ../log
import ../request
import ../utils
import ../dcs as base
import ./zookeeper

export base, zookeeper

let logger = getLogger("patroni.dcs.exhibitor")

type
  ExhibitorEnsembleProvider* = ref object
    ## Provider for ZooKeeper ensemble from Exhibitor.
    exhibitorPort: int
    uriPath: string
    pollInterval: int
    exhibitors: seq[string]
    bootExhibitors: seq[string]
    zookeeperHosts: string
    nextPoll: Option[float]

const
  TIMEOUT = 3.1

proc newExhibitorEnsembleProvider*(hosts: seq[string], port: int,
                                    uriPath: string = "/exhibitor/v1/cluster/list",
                                    pollInterval: int = 300): ExhibitorEnsembleProvider =
  ## Create a new ExhibitorEnsembleProvider.
  ##
  ## :param hosts: List of Exhibitor hosts.
  ## :param port: Exhibitor port.
  ## :param uriPath: URI path for cluster list endpoint.
  ## :param pollInterval: How often to poll for ensemble changes.
  new(result)
  result.exhibitorPort = port
  result.uriPath = uriPath
  result.pollInterval = pollInterval
  result.exhibitors = hosts
  result.bootExhibitors = hosts
  result.zookeeperHosts = ""
  result.nextPoll = none(float)

  # Wait for initial ensemble
  while not result.poll():
    logger.info("waiting on exhibitor")
    sleep(5000)

proc queryExhibitors(self: ExhibitorEnsembleProvider, exhibitors: seq[string]): Option[JsonNode] =
  ## Query Exhibitor hosts for ensemble information.
  var hosts = exhibitors
  shuffle(hosts)

  for host in hosts:
    try:
      let url = fmt"http://{host}:{self.exhibitorPort}{self.uriPath}"
      let response = httpGet(url, timeout = TIMEOUT)
      if response.isSome:
        let data = parseJson(response.get)
        return some(data)
    except:
      logger.debug(fmt"Request to {host} failed")

  return none(JsonNode)

proc poll*(self: ExhibitorEnsembleProvider): bool =
  ## Poll for ensemble changes.
  ##
  ## :returns: true if ensemble changed.
  if self.nextPoll.isSome and self.nextPoll.get > epochTime():
    return false

  var jsonData = self.queryExhibitors(self.exhibitors)
  if jsonData.isNone:
    jsonData = self.queryExhibitors(self.bootExhibitors)

  if jsonData.isSome and jsonData.get.hasKey("servers") and jsonData.get.hasKey("port"):
    self.nextPoll = some(epochTime() + float(self.pollInterval))

    let data = jsonData.get
    var servers: seq[string] = @[]
    for server in data["servers"]:
      servers.add(server.getStr())

    let port = $data["port"].getInt()
    let zookeeperHosts = servers.sorted().mapIt(it & ":" & port).join(",")

    if self.zookeeperHosts != zookeeperHosts:
      logger.info(fmt"ZooKeeper connection string has changed: {self.zookeeperHosts} => {zookeeperHosts}")
      self.zookeeperHosts = zookeeperHosts
      self.exhibitors = servers
      return true

  return false

proc getZookeeperHosts*(self: ExhibitorEnsembleProvider): string =
  ## Get current ZooKeeper connection string.
  result = self.zookeeperHosts

type
  Exhibitor* = ref object of ZooKeeper
    ## ZooKeeper DCS with Exhibitor ensemble discovery.
    ensembleProvider: ExhibitorEnsembleProvider

proc newExhibitor*(config: JsonNode): Exhibitor =
  ## Create a new Exhibitor DCS instance.
  new(result)

  let exhibSection = if config.hasKey("exhibitor"): config["exhibitor"] else: newJObject()

  var hosts: seq[string] = @[]
  if exhibSection.hasKey("hosts"):
    for h in exhibSection["hosts"]:
      hosts.add(h.getStr())

  let port = exhibSection.getOrDefault("port").getInt(8181)
  let pollInterval = exhibSection.getOrDefault("poll_interval").getInt(300)

  result.ensembleProvider = newExhibitorEnsembleProvider(hosts, port, pollInterval = pollInterval)

  # Create a modified config with resolved ZooKeeper hosts
  var zkConfig = copy(config)
  if not zkConfig.hasKey("zookeeper"):
    zkConfig["zookeeper"] = newJObject()
  zkConfig["zookeeper"]["hosts"] = %result.ensembleProvider.getZookeeperHosts()

  # Initialize the ZooKeeper base
  initAbstractDCS(result, zkConfig)

  var zkHosts = result.ensembleProvider.getZookeeperHosts()
  result.client = newZKClient(zkHosts)
  result.ttl = config["ttl"].getInt(30)
  result.hasFailed = false
  result.doNotWatch = false
  result.lastLeaderVersion = 0

method loadCluster*(self: Exhibitor, path: string): Cluster =
  ## Load cluster from ZooKeeper, polling Exhibitor first.
  if self.ensembleProvider.poll():
    # Ensemble changed, reconnect
    let newHosts = self.ensembleProvider.getZookeeperHosts()
    self.client.close()
    self.client = newZKClient(newHosts)
    discard self.client.connect()

  # Call parent implementation
  result = procCall ZooKeeper(self).loadCluster(path)

