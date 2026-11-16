println "Inspecting ${args.name}"
println "Application loader: ${application}"
return [runtime: jmx.read('java.lang:type=Runtime', 'Name'), extensions: extensions.keySet()]
