
Telepresence Puppet
===================

Project Description
-------------------
This bit of code is supposed to allow one to remotely control a computer
with a camera and some motors. Let's see how it turns out!

It uses the incredible https://webrtc.org/ and the https://github.com/priologic/easyrtc
Javascript libaries to make it all happen.

Major inspiration and ideas came from:
  * William Cooley's video https://youtu.be/rtgysHYEnNo
  * ArchReactors telebotv2 https://github.com/ArchReactor/telebotv2

Software Environment Setup Notes
---------------------------------
#### 1. Install NodeJS, v6
  * Linux:
    * curl -sL https://deb.nodesource.com/setup_6.x | sudo -E bash -
    * sudo apt-get install -y nodejs
    * check with node -v. Should say v6.10.0 or greater
  * Windows:
    * Use installer from nodejs.org
#### 2. Clone EasyRTC
  * git clone https://github.com/priologic/easyrtc.git
  * put it at the same directory level as this tpp code.
#### 3. Change directory to easyRTC, and follow their installation instructions
  * npm install in a couple of places
#### 4. Create self signed key and certs
  * In the directory tpp/server, create a key and cert.
  * openssl genrsa -out domain.key 2048
  * openssl req -new -x509 -key domain.key -out domain.crt -days 3650 -subj /CN=www.example.com
  * run npm install in tpp/server to install dependencies.
  

Running
---------------------------------
* On Remote Robot
  * Run server
    * cd server
    * node server_ssl.js
  * Open the Chrome browser and navigate to https://localhost:8443/robot.html
* On Client computer
  * open Chrome to https://ip.of.remote.com:8443/?username=remote
  * Connect using the connect to button on the lower right corner.
  
Caviots, Assumptions, Limitations
----------------------------------
* Only works when computers are on the same network. 
  * I don't want to deal with the complexities of that yet.

Status
----------------------------------
#### 2018-03-38
  Lot of small tweaks. I'm using it lots for my remote work.
  Still not moving around, only pan/tilt camera in my cubical right now.

#### 2017-09-30
  On screen mouse controls working.
  Add map function to scale input to needed output ranges.
  
#### 2017-08-05
  Switching development over to Linux.
  Basic serial port commands are working, need to finish maestro driver
  
#### 2017-04-24
  Discovered node-pololumaestro. It looks excellent, but
  unfortunately it hasn't be updated in 3 years and doesn't install
  for me.
  
  I'll see if I can quickly create a simple driver for the maestro. I
  don't have the JavaScript/Node skills to update the package.

To Do List
----------
- [X] Add instructions on creating certs to README.md.
- [X] Figure out CSS to arrange the web page the way I want it.
- [X] Get WebRTC data channel plumbed and working.
- [X] Incorporate virtual joystick for control of camera, motors, etc.
- [X] Figure out if possible to set name instead of using the randomly generated name.
- [X] Get the serial port working via nodejs.
- [X] Implement web to serial connection.
- [X] Write code for controlling servos.
- [ ] Write code for controlling motors.
- [X] Set default remote name to something like "remote_bot"
- [X] Add remote control of volume.
- [ ] Implement heartbeat/disconnect detection.
- [ ] Implement disconnect handling on client side.
- [ ] Put some sort of mobile base together so I have something to control.
- [ ] It looks like remote speakers will be helpful.
- [ ] Consider external microphone.
- [ ] Figure out STUN and TURN servers so it can be used through NAT and firewalls
- [ ] Add some sort of authentication
- [ ] Fix certificate to avoid browser warnings.

