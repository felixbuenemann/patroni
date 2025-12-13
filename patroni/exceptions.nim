## Implement high-level Patroni exceptions.
##
## More specific exceptions can be found in other modules, as subclasses of any exception defined in this module.

type
  PatroniException* = object of CatchableError
    ## Parent class for all kind of Patroni exceptions.
    ##
    ## :ivar value: description of the exception.
    value*: string

  PatroniFatalException* = object of PatroniException
    ## Catastrophic exception that prevents Patroni from performing its job.

  PostgresException* = object of PatroniException
    ## Any exception related with Postgres management.

  DCSError* = object of PatroniException
    ## Parent class for all kind of DCS related exceptions.

  PostgresConnectionException* = object of PostgresException
    ## Any problem faced while connecting to a Postgres instance.

  WatchdogError* = object of PatroniException
    ## Any problem faced while managing a watchdog device.

  ConfigParseError* = object of PatroniException
    ## Any issue identified while loading or validating the Patroni configuration.

  PatroniAssertionError* = object of PatroniException
    ## Any issue related to type/value validation.

proc newPatroniException*(value: string): ref PatroniException =
  ## Create a new instance of PatroniException with the given description.
  ##
  ## :param value: description of the exception.
  new(result)
  result.value = value
  result.msg = value

proc newPatroniFatalException*(value: string): ref PatroniFatalException =
  ## Create a new instance of PatroniFatalException.
  new(result)
  result.value = value
  result.msg = value

proc newPostgresException*(value: string): ref PostgresException =
  ## Create a new instance of PostgresException.
  new(result)
  result.value = value
  result.msg = value

proc newDCSError*(value: string): ref DCSError =
  ## Create a new instance of DCSError.
  new(result)
  result.value = value
  result.msg = value

proc newPostgresConnectionException*(value: string): ref PostgresConnectionException =
  ## Create a new instance of PostgresConnectionException.
  new(result)
  result.value = value
  result.msg = value

proc newWatchdogError*(value: string): ref WatchdogError =
  ## Create a new instance of WatchdogError.
  new(result)
  result.value = value
  result.msg = value

proc newConfigParseError*(value: string): ref ConfigParseError =
  ## Create a new instance of ConfigParseError.
  new(result)
  result.value = value
  result.msg = value

proc newPatroniAssertionError*(value: string): ref PatroniAssertionError =
  ## Create a new instance of PatroniAssertionError.
  new(result)
  result.value = value
  result.msg = value
