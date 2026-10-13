export namespace adb {
	
	export class Device {
	    serial: string;
	    state: string;
	    model: string;
	    wifi: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Device(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serial = source["serial"];
	        this.state = source["state"];
	        this.model = source["model"];
	        this.wifi = source["wifi"];
	    }
	}
	export class MDNSService {
	    name: string;
	    type: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new MDNSService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.address = source["address"];
	    }
	}

}

export namespace main {
	
	export class EnableWiFiResult {
	    ip: string;
	    address: string;
	    connected: boolean;
	    needsPairing: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new EnableWiFiResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ip = source["ip"];
	        this.address = source["address"];
	        this.connected = source["connected"];
	        this.needsPairing = source["needsPairing"];
	        this.message = source["message"];
	    }
	}
	export class EnvInfo {
	    os: string;
	    adbPath: string;
	    adbVersion: string;
	    adbError: string;
	    serverVersion: string;
	    demoAvailable: boolean;
	    debug: boolean;
	    autostart: string;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new EnvInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.os = source["os"];
	        this.adbPath = source["adbPath"];
	        this.adbVersion = source["adbVersion"];
	        this.adbError = source["adbError"];
	        this.serverVersion = source["serverVersion"];
	        this.demoAvailable = source["demoAvailable"];
	        this.debug = source["debug"];
	        this.autostart = source["autostart"];
	        this.dataDir = source["dataDir"];
	    }
	}
	export class MirrorInfo {
	    url: string;
	    deviceName: string;
	    control: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MirrorInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.deviceName = source["deviceName"];
	        this.control = source["control"];
	    }
	}
	export class Settings {
	    maxSize: number;
	    maxFps: number;
	    bitRateMbps: number;
	    control: boolean;
	    stayAwake: boolean;
	    audio: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxSize = source["maxSize"];
	        this.maxFps = source["maxFps"];
	        this.bitRateMbps = source["bitRateMbps"];
	        this.control = source["control"];
	        this.stayAwake = source["stayAwake"];
	        this.audio = source["audio"];
	    }
	}

}

