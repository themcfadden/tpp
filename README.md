
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
  * git clone git@github.com:priologic/easyrtc.git
  * put it at the same directory level as this tpp code.
#### 3. Change directory to easyRTC, and follow their installation instructions
  * npm install in a couple of places
#### 4. Create self signed key and certs
  * In the directory tpp/server, create a key and cert.
  * openssl genrsa -out domain.key 2048
  * openssl req -new -x509 -key domain.key -out domain.crt -days 3650 -subj /CN=www.example.com

Running
---------------------------------
* On Remote Robot
  * Run server
    * cd server
    * node server_ssl.js
  * Open the Chrome browser and navigate to https://localhost:8443/remote.html
* On Client computer
  * open Chrome to https://ip.of.remote.com:8443
  * Connect using the connect to button on the lower right corner.
  
Caviots, Assumptions, Limitations
----------------------------------
* Only works when computers are on the same network. 
  * I don't want to deal with the complexities of that yet.

  
To Do List
----------
- [X] Add instructions on creating certs to README.md.
- [X] Figure out CSS to arrange the web page the way I want it.
- [ ] Incorporate virtual joystick for control of camera, motors, etc.
- [X] Figure out if possible to set name instead of using the randomly generated name.
- [ ] Get the serial port working via nodejs.
- [ ] Write code for controlling servos.
- [ ] Write code for controlling motors.
- [ ] Set default remote name to something like "remote_bot"
- [ ] Implement disconnect on client side.
